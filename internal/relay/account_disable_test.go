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
		{
			name: "openai invalid api key message only",
			body: []byte(`{"error":{"message":"invalid api key"}}`),
			want: "invalid_api_key",
		},
		{
			name: "chinese api key missing",
			body: []byte(`{"error":{"message":"apiKey不存在或者配置错误"}}`),
			want: "apikey",
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

func TestClassifyUpstreamErrorChineseAuthTerminal(t *testing.T) {
	body := []byte(`{"error":{"message":"apiKey不存在或者配置错误"}}`)
	if got := ClassifyUpstreamError(fasthttp.StatusForbidden, body); got != ErrAccountTerminal {
		t.Fatalf("ClassifyUpstreamError = %s, want account_terminal", got.String())
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
		{
			name:   "401 generic unauthorized",
			status: fasthttp.StatusUnauthorized,
			body:   []byte(`{"error":{"message":"unauthorized"}}`),
		},
		{
			name:   "structured insufficient quota",
			status: fasthttp.StatusPaymentRequired,
			body:   []byte(`{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`),
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

func TestAccountPoolPickExcludingSkipsOnlyForCurrentRequest(t *testing.T) {
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc1, acc2})

	got, ok := pool.PickExcluding(map[string]bool{acc1.ID.String(): true})
	if !ok || got.ID != acc2.ID {
		t.Fatalf("PickExcluding = (%v,%v), want second account", got, ok)
	}
	if count := pool.AvailableCount(); count != 2 {
		t.Fatalf("PickExcluding must not mutate global availability, got %d", count)
	}
	if _, ok := pool.PickByID(acc1.ID.String()); !ok {
		t.Fatalf("excluded account must remain globally available")
	}
}

func TestAccountPoolPickForModelAvoidsOverloadedAccount(t *testing.T) {
	loaded := &db.Account{Base: db.Base{ID: uuid.New()}, Name: "loaded", Enabled: true, Weight: 100}
	available := &db.Account{Base: db.Base{ID: uuid.New()}, Name: "available", Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{loaded, available})
	for i := 0; i < accountInFlightSoftCap; i++ {
		pool.Begin(loaded.ID.String())
	}

	for i := 0; i < 5; i++ {
		got, ok := pool.PickForModel("model", nil)
		if !ok {
			t.Fatal("PickForModel returned no account")
		}
		if got.ID != available.ID {
			t.Fatalf("PickForModel picked overloaded account %q", got.Name)
		}
	}
}

func TestAccountPoolInFlightBeginEnd(t *testing.T) {
	acc := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc})

	if got := pool.Begin(acc.ID.String()); got != 1 {
		t.Fatalf("Begin = %d, want 1", got)
	}
	if got := pool.InFlight(acc.ID.String()); got != 1 {
		t.Fatalf("InFlight = %d, want 1", got)
	}
	if got := pool.End(acc.ID.String()); got != 0 {
		t.Fatalf("End = %d, want 0", got)
	}
	if got := pool.End(acc.ID.String()); got != 0 {
		t.Fatalf("extra End = %d, want 0", got)
	}
}

func TestPoolManagerSetPoolPreservesAndReleasesInFlight(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}}
	acc := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1}
	pm := NewPoolManager()
	oldPool := NewAccountPool([]*db.Account{acc})
	pm.SetPool(ch.ID.String(), oldPool)
	relayer := &Relayer{pools: pm}

	release := relayer.beginAccountAttempt(ch, acc, nil, "test")
	newPool := NewAccountPool([]*db.Account{acc})
	pm.SetPool(ch.ID.String(), newPool)

	if got := newPool.InFlight(acc.ID.String()); got != 1 {
		t.Fatalf("replacement pool in-flight = %d, want 1", got)
	}
	release()
	if got := newPool.InFlight(acc.ID.String()); got != 0 {
		t.Fatalf("released replacement pool in-flight = %d, want 0", got)
	}
}

func TestTerminalAccountErrorDisablesAndEvicts(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, APIFormat: "codex"}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc1.ID.String(), 60)
	policy := NewCooldownPolicy()
	defer policy.Close()
	policy.ComputeCooldown(ErrAccountSide, fasthttp.StatusTooManyRequests, acc1.ID.String())
	relayer := &Relayer{pools: pools, affinity: affinity, cooldownPolicy: policy}

	relayer.disableAndEvict(ch, acc1, "permission_denied")

	if acc1.Enabled {
		t.Fatalf("terminal account error must hard-disable the account")
	}
	if got, _ := acc1.Metadata["disabled_reason"].(string); got != "permission_denied" {
		t.Fatalf("disabled_reason = %q, want permission_denied", got)
	}
	if got, _ := acc1.Metadata["auto_disable_reason"].(string); got != "permission_denied" {
		t.Fatalf("auto_disable_reason = %q, want permission_denied", got)
	}
	if _, ok := affinity.Get("token", "model", "scope"); ok != "" {
		t.Fatalf("terminal disable must clear account affinity")
	}
	if len(policy.Snapshot()) != 0 {
		t.Fatalf("terminal disable must reset cooldown policy state")
	}
	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("disabled terminal-error account must not be selected")
	}
	if got, ok := pool.PickByID(acc2.ID.String()); !ok || got.ID != acc2.ID {
		t.Fatalf("healthy account should remain selectable")
	}
}

func TestAPIKeyAccountSideFailureCoolsPoolWithoutPersistingCooldown(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, Type: "openai", APIFormat: "standard"}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc1.ID.String(), 60)
	policy := NewCooldownPolicy()
	defer policy.Close()
	relayer := &Relayer{pools: pools, affinity: affinity, cooldownPolicy: policy}

	relayer.prepareAccountFailover(ch, acc1, fasthttp.StatusTooManyRequests, []byte(`{"error":"rate limit"}`), true)

	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("API key account-side failure must cooldown account in pool")
	}
	if got, ok := pool.PickByID(acc2.ID.String()); !ok || got.ID != acc2.ID {
		t.Fatalf("healthy account should remain selectable")
	}
	if acc1.CooldownUntil == nil {
		t.Fatalf("account cooldown must be visible in runtime account")
	}
	if got, _ := acc1.Metadata["auto_disable_reason"].(string); got != "" {
		t.Fatalf("API key transient failure must not mark auto_disable_reason, got %q", got)
	}
	if gotCh, gotAcc := affinity.Get("token", "model", "scope"); gotCh != "" || gotAcc != "" {
		t.Fatalf("account cooldown must evict affinity, got (%q,%q)", gotCh, gotAcc)
	}
}

func TestAPIKeyGenericUnauthorizedCoolsAccountWithoutDisabling(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, Type: "openai", APIFormat: "standard"}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc1.ID.String(), 60)
	policy := NewCooldownPolicy()
	defer policy.Close()
	relayer := &Relayer{pools: pools, affinity: affinity, cooldownPolicy: policy}

	relayer.prepareAccountFailover(ch, acc1, fasthttp.StatusUnauthorized, []byte(`{"error":{"message":"unauthorized"}}`), false)

	if !acc1.Enabled {
		t.Fatalf("generic API key 401 must not permanently disable account")
	}
	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("generic API key 401 must cooldown account in pool")
	}
	if got, _ := acc1.Metadata["disabled_reason"].(string); got != "" {
		t.Fatalf("generic API key 401 must not set disabled_reason, got %q", got)
	}
	if got, _ := acc1.Metadata["auto_disable_reason"].(string); got != "" {
		t.Fatalf("generic API key 401 must not set auto_disable_reason, got %q", got)
	}
	if got, ok := pool.PickByID(acc2.ID.String()); !ok || got.ID != acc2.ID {
		t.Fatalf("healthy account should remain selectable")
	}
	if gotCh, gotAcc := affinity.Get("token", "model", "scope"); gotCh != "" || gotAcc != "" {
		t.Fatalf("generic API key 401 must evict affinity, got (%q,%q)", gotCh, gotAcc)
	}
}

func TestAPIKeyTerminalAuthFailureDisablesAccount(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, Type: "openai", APIFormat: "standard"}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, CredType: "api_key"}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc1.ID.String(), 60)
	policy := NewCooldownPolicy()
	defer policy.Close()
	relayer := &Relayer{pools: pools, affinity: affinity, cooldownPolicy: policy}

	relayer.prepareAccountFailover(ch, acc1, fasthttp.StatusUnauthorized, []byte(`{"error":{"code":"invalid_api_key"}}`), false)

	if acc1.Enabled {
		t.Fatalf("API key terminal auth failure must disable account")
	}
	if _, ok := pool.PickByID(acc1.ID.String()); ok {
		t.Fatalf("disabled API key account must not be selectable")
	}
	if got, _ := acc1.Metadata["disabled_reason"].(string); got != "invalid_api_key" {
		t.Fatalf("disabled_reason = %q, want invalid_api_key", got)
	}
	if got, _ := acc1.Metadata["auth_failure_next_action"].(string); got != "disabled" {
		t.Fatalf("auth_failure_next_action = %q, want disabled", got)
	}
	if got, ok := pool.PickByID(acc2.ID.String()); !ok || got.ID != acc2.ID {
		t.Fatalf("healthy account should remain selectable")
	}
	if gotCh, gotAcc := affinity.Get("token", "model", "scope"); gotCh != "" || gotAcc != "" {
		t.Fatalf("terminal auth failure must evict affinity, got (%q,%q)", gotCh, gotAcc)
	}
}

func TestServerSideChannelFailoverDoesNotEvictAffinity(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, APIFormat: "codex"}
	acc := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc.ID.String(), 60)
	relayer := &Relayer{affinity: affinity, channelModelBlock: NewChannelModelBlocklist(0)}

	relayer.prepareChannelFailover(ch, fasthttp.StatusBadGateway, []byte("temporary upstream failure"), "model")

	gotCh, gotAcc := affinity.Get("token", "model", "scope")
	if gotCh != ch.ID.String() || gotAcc != acc.ID.String() {
		t.Fatalf("server-side failover must preserve affinity, got (%q,%q)", gotCh, gotAcc)
	}
}

func TestConfigSideChannelFailoverEvictsAffinity(t *testing.T) {
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, APIFormat: "codex"}
	acc := &db.Account{Base: db.Base{ID: uuid.New()}, Enabled: true, Weight: 1}
	affinity := NewAffinityCache()
	affinity.Set("token", "model", "scope", ch.ID.String(), acc.ID.String(), 60)
	relayer := &Relayer{affinity: affinity, channelModelBlock: NewChannelModelBlocklist(0)}

	relayer.prepareChannelFailover(ch, fasthttp.StatusNotFound, []byte("model not found"), "model")

	if gotCh, gotAcc := affinity.Get("token", "model", "scope"); gotCh != "" || gotAcc != "" {
		t.Fatalf("config-side failover must evict channel affinity, got (%q,%q)", gotCh, gotAcc)
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

func TestAffinityCacheEvictsOnlyFailedAccount(t *testing.T) {
	cache := NewAffinityCache()
	cache.Set("token-1", "gpt-5.5", "session-a", "channel-1", "account-1", 60)
	cache.Set("token-1", "gpt-5.5", "session-b", "channel-1", "account-2", 60)
	cache.Set("token-1", "gpt-5.5", "session-c", "channel-2", "account-1", 60)

	// EvictAccount now takes only accountID; removes ALL entries for that account across channels
	cache.EvictAccount("account-1")

	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-a"); ch != "" || acc != "" {
		t.Fatalf("failed account affinity on ch1 should be evicted, got (%q,%q)", ch, acc)
	}
	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-b"); ch != "channel-1" || acc != "account-2" {
		t.Fatalf("other account affinity = (%q,%q), want channel-1/account-2", ch, acc)
	}
	if ch, acc := cache.Get("token-1", "gpt-5.5", "session-c"); ch != "" || acc != "" {
		t.Fatalf("same account on another channel should also be evicted, got (%q,%q)", ch, acc)
	}
}
