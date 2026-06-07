package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func TestTerminalAccountDisableReasonDetectsStructuredForbiddenFields(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "cockpit quota forbidden",
			body: []byte(`{"quota":{"is_forbidden":true}}`),
			want: "account_forbidden",
		},
		{
			name: "quota fetch forbidden sentinel",
			body: []byte(`{"_forbidden":true,"_forbidden_reason":"account_forbidden"}`),
			want: "account_forbidden",
		},
		{
			name: "gemini permission denied",
			body: []byte(`{"error":{"code":403,"message":"API returned 403 Forbidden, account has no access to Gemini Code Assist","status":"PERMISSION_DENIED"}}`),
			want: "permission_denied",
		},
		{
			name: "openai invalid api key",
			body: []byte(`{"error":{"message":"Incorrect API key provided","code":"invalid_api_key","type":"invalid_request_error"}}`),
			want: "invalid_api_key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := terminalAccountDisableReason(fasthttp.StatusForbidden, tc.body)
			if !ok {
				t.Fatalf("terminalAccountDisableReason did not detect terminal error")
			}
			if got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTerminalAccountDisableReasonDoesNotDisableTransientQuotaOrUserErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   []byte
	}{
		{
			name:   "429 resource exhausted",
			status: fasthttp.StatusTooManyRequests,
			body:   []byte(`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`),
		},
		{
			name:   "403 policy rejection without account marker",
			status: fasthttp.StatusForbidden,
			body:   []byte(`{"error":{"message":"safety policy blocked this request","type":"invalid_request_error"}}`),
		},
		{
			name:   "400 invalid request",
			status: fasthttp.StatusBadRequest,
			body:   []byte(`{"error":{"message":"messages must not be empty","type":"invalid_request_error"}}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := terminalAccountDisableReason(tc.status, tc.body); ok {
				t.Fatalf("terminalAccountDisableReason = %q, want no disable", got)
			}
		})
	}
}

func TestAccountPoolDisableRemovesAccountFromSelection(t *testing.T) {
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pool.Disable(acc1.ID.String())

	for i := 0; i < 5; i++ {
		got, ok := pool.Pick()
		if !ok {
			t.Fatalf("Pick() returned no account")
		}
		if got.ID == acc1.ID {
			t.Fatalf("disabled account was selected")
		}
	}
}

func TestAccountPoolPickByIDRequiresAvailableWeight(t *testing.T) {
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc1, acc2})

	if got, ok := pool.PickByID(acc1.ID.String()); !ok || got.ID != acc1.ID {
		t.Fatalf("PickByID did not return available affinity account")
	}
	if count := pool.AvailableCount(); count != 2 {
		t.Fatalf("AvailableCount = %d, want 2", count)
	}

	pool.Cooldown(acc1.ID.String(), time.Hour)
	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("cooled-down affinity account must not be selected")
	}
	if count := pool.AvailableCount(); count != 1 {
		t.Fatalf("AvailableCount after cooldown = %d, want 1", count)
	}
}

func TestTerminalAccountErrorCoolsDownWithoutDisabling(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	relayer := &Relayer{pools: pools}

	relayer.cooldownAccountOnTerminalUpstreamError(ch, acc1, fasthttp.StatusForbidden, []byte(`{"error":{"status":"PERMISSION_DENIED","message":"permission denied"}}`))

	if !acc1.Enabled {
		t.Fatalf("terminal account error must not hard-disable the account")
	}
	if got, _ := acc1.Metadata["auto_disable_reason"].(string); got != "permission_denied" {
		t.Fatalf("auto_disable_reason = %q, want permission_denied", got)
	}
	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("cooled-down terminal-error account must not be selected")
	}
	if got, ok := pool.PickByID(acc2.ID.String()); !ok || got.ID != acc2.ID {
		t.Fatalf("healthy account should remain selectable")
	}
}

func TestAffinityCacheStoresSessionScopedAccount(t *testing.T) {
	cache := NewAffinityCache()
	cache.Set("token-1", "gpt-5.5", "session-a", "channel-1", "account-1", 60)
	cache.Set("token-1", "gpt-5.5", "session-b", "channel-1", "account-2", 60)

	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-a"); ch != "channel-1" || acc != "account-1" {
		t.Fatalf("session-a affinity = (%q,%q)", ch, acc)
	}
	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-b"); ch != "channel-1" || acc != "account-2" {
		t.Fatalf("session-b affinity = (%q,%q)", ch, acc)
	}
	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-c"); ch != "" || acc != "" {
		t.Fatalf("unexpected affinity miss value = (%q,%q)", ch, acc)
	}
}
