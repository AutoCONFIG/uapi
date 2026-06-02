package anthropic

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestParseUsageFullTotalsNestedCacheCreation(t *testing.T) {
	a := &AnthropicAdaptor{}
	usage, err := a.ParseUsageFull([]byte(`{"usage":{"input_tokens":10,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4},"cache_read_input_tokens":5}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 2 {
		t.Fatalf("tokens = (%d,%d), want (10,2)", usage.PromptTokens, usage.CompletionTokens)
	}
	if usage.CacheCreationInputTokens != 7 {
		t.Fatalf("CacheCreationInputTokens = %d, want 7", usage.CacheCreationInputTokens)
	}
	if usage.CacheReadInputTokens != 5 {
		t.Fatalf("CacheReadInputTokens = %d, want 5", usage.CacheReadInputTokens)
	}
}

func TestGetRequestURLAlwaysUsesMessagesEndpoint(t *testing.T) {
	a := &AnthropicAdaptor{}
	a.Init(&db.Channel{Endpoint: "https://api.anthropic.com/v1"}, nil)

	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1beta/models/test:generateContent",
		"/v1/messages",
	} {
		got, err := a.GetRequestURL(path)
		if err != nil {
			t.Fatalf("GetRequestURL(%q): %v", path, err)
		}
		if got != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("GetRequestURL(%q) = %q", path, got)
		}
	}
}

func TestClaudeCodeOAuthSetupRequestHeaderUsesNativeContract(t *testing.T) {
	a := &AnthropicAdaptor{}
	a.Init(&db.Channel{Type: "anthropic", APIFormat: "claude_code", Endpoint: "https://api.anthropic.com/v1"}, &db.Account{CredType: "oauth_token"})

	var req fasthttp.Request
	if err := a.SetupRequestHeader(&req, "oauth-access-token"); err != nil {
		t.Fatalf("SetupRequestHeader: %v", err)
	}
	wants := map[string]string{
		"Authorization":            "Bearer oauth-access-token",
		"anthropic-beta":           OAuthBetaHeader,
		"x-app":                    "cli",
		"User-Agent":               ClaudeCLIUserAgent,
		"X-Claude-Code-Session-Id": ClaudeCodeSessionID,
		"anthropic-version":        "2023-06-01",
		"Content-Type":             "application/json",
	}
	for header, want := range wants {
		if got := string(req.Header.Peek(header)); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if got := string(req.Header.Peek("x-api-key")); got != "" {
		t.Fatalf("x-api-key = %q, want empty for OAuth", got)
	}
}
