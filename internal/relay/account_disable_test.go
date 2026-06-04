package relay

import (
	"testing"

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
