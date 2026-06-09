package relay

import (
	"testing"
)

func TestClassifyUpstreamError_ServerSide(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"408 timeout", 408, ""},
		{"500 internal", 500, `{"error":"internal"}`},
		{"502 bad gateway", 502, ""},
		{"503 service unavailable", 503, ""},
		{"504 gateway timeout", 504, ""},
		{"599 custom", 599, ""},
		{"401 without auth keyword", 401, `{"error":{"message":"backend down"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyUpstreamError(c.statusCode, []byte(c.body))
			if got != ErrServerSide {
				t.Errorf("expected ErrServerSide, got %s", got)
			}
		})
	}
}

func TestClassifyUpstreamError_AccountSide(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"401 with auth keyword", 401, `{"error":{"message":"invalid api key"}}`},
		{"401 with unauthorized", 401, `{"error":{"code":"unauthorized"}}`},
		{"402 payment required", 402, `{"error":"insufficient_quota"}`},
		{"429 rate limit", 429, `{"error":"rate_limit_exceeded"}`},
		{"403 forbidden generic", 403, `{"error":"forbidden"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyUpstreamError(c.statusCode, []byte(c.body))
			if got != ErrAccountSide {
				t.Errorf("expected ErrAccountSide, got %s", got)
			}
		})
	}
}

func TestClassifyUpstreamError_AccountTerminal(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"invalid_grant", 401, `{"error":"invalid_grant"}`},
		{"token_revoked", 401, `{"error":{"code":"token_revoked"}}`},
		{"refresh_token_expired", 401, `{"error_description":"refresh_token_expired"}`},
		{"account_suspended", 403, `{"error":"account_suspended"}`},
		{"account banned", 403, `{"error":"account banned"}`},
		{"api_key_revoked", 401, `{"error":{"code":"api_key_revoked"}}`},
		{"organization disabled", 403, `{"error":{"message":"organization has been disabled"}}`},
		{"PERMISSION_DENIED status", 403, `{"error":{"status":"PERMISSION_DENIED"}}`},
		{"is_forbidden flag", 403, `{"is_forbidden":true,"_forbidden_reason":"banned"}`},
		{"invalid_api_key code", 401, `{"error":{"code":"invalid_api_key"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyUpstreamError(c.statusCode, []byte(c.body))
			if got != ErrAccountTerminal {
				t.Errorf("expected ErrAccountTerminal, got %s; body=%s", got, c.body)
			}
		})
	}
}

func TestClassifyUpstreamError_ConfigSide(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"404 model not found", 404, `{"error":{"message":"model gpt-99 not found"}}`},
		{"404 model does not exist", 404, `{"error":"model does not exist"}`},
		{"404 not_supported", 404, `{"error":"model not_supported"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyUpstreamError(c.statusCode, []byte(c.body))
			if got != ErrConfigSide {
				t.Errorf("expected ErrConfigSide, got %s", got)
			}
		})
	}
}

func TestClassifyUpstreamError_ClientSide(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"400 bad request", 400, `{"error":"bad request"}`},
		{"422 unprocessable", 422, `{"error":"invalid params"}`},
		{"404 without model keyword", 404, `{"error":"route not found"}`},
		{"405 method not allowed", 405, ``},
		{"413 payload too large", 413, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyUpstreamError(c.statusCode, []byte(c.body))
			if got != ErrClientSide {
				t.Errorf("expected ErrClientSide, got %s", got)
			}
		})
	}
}

func TestIsTerminalAuthError(t *testing.T) {
	terminalCases := []struct {
		statusCode int
		body       string
	}{
		{401, `{"error":"invalid_grant"}`},
		{401, `{"error":"token_revoked"}`},
		{403, `{"error":"account_suspended"}`},
		{403, `{"is_forbidden":true}`},
		{401, `{"error":{"code":"invalid_api_key"}}`},
	}
	for _, c := range terminalCases {
		if !IsTerminalAuthError(c.statusCode, []byte(c.body)) {
			t.Errorf("expected terminal: code=%d body=%s", c.statusCode, c.body)
		}
	}

	nonTerminalCases := []struct {
		statusCode int
		body       string
	}{
		{429, `{"error":"rate_limit"}`},
		{500, `{"error":"server"}`},
		{401, `{"error":"temporarily unavailable"}`},
		{402, `{"error":"quota exhausted"}`}, // 配额，不是终态
	}
	for _, c := range nonTerminalCases {
		if IsTerminalAuthError(c.statusCode, []byte(c.body)) {
			t.Errorf("expected non-terminal: code=%d body=%s", c.statusCode, c.body)
		}
	}
}

func TestIsQuotaError(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"error":"insufficient_quota"}`, true},
		{`{"error":"rate_limit_exceeded"}`, true},
		{`{"error":{"message":"You exceeded your quota"}}`, true},
		{`{"error":"billing not configured"}`, true},
		{`{"error":"too many requests"}`, true},
		{`{"error":"invalid_grant"}`, false},
		{`{"error":"internal"}`, false},
		{``, false},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			got := IsQuotaError([]byte(c.body))
			if got != c.want {
				t.Errorf("body=%s want=%v got=%v", c.body, c.want, got)
			}
		})
	}
}

func TestErrorClass_String(t *testing.T) {
	if ErrServerSide.String() != "server_side" {
		t.Error("ErrServerSide string mismatch")
	}
	if ErrAccountSide.String() != "account_side" {
		t.Error("ErrAccountSide string mismatch")
	}
	if ErrAccountTerminal.String() != "account_terminal" {
		t.Error("ErrAccountTerminal string mismatch")
	}
	if ErrConfigSide.String() != "config_side" {
		t.Error("ErrConfigSide string mismatch")
	}
	if ErrClientSide.String() != "client_side" {
		t.Error("ErrClientSide string mismatch")
	}
	if ErrUnknown.String() != "unknown" {
		t.Error("ErrUnknown string mismatch")
	}
}

func TestClassifyUpstreamError_EmptyBody(t *testing.T) {
	// 空 body 时仅按状态码判定
	if ClassifyUpstreamError(500, nil) != ErrServerSide {
		t.Error("500 nil body should be ServerSide")
	}
	if ClassifyUpstreamError(429, nil) != ErrAccountSide {
		t.Error("429 nil body should be AccountSide")
	}
	if ClassifyUpstreamError(401, nil) != ErrAccountSide {
		t.Error("401 nil body should default to AccountSide")
	}
	if ClassifyUpstreamError(404, nil) != ErrClientSide {
		t.Error("404 nil body should be ClientSide (no model keyword)")
	}
}
