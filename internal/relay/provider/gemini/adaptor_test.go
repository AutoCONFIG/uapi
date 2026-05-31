package gemini

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestSetupRequestHeaderUsesBearerForOAuthToken(t *testing.T) {
	adaptor := &GeminiAdaptor{}
	adaptor.Init(&db.Channel{Endpoint: "https://example.test"}, &db.Account{CredType: "oauth_token"})
	adaptor.SetRequestParams("gemini-2.5-pro", false)

	var req fasthttp.Request
	if err := adaptor.SetupRequestHeader(&req, "ya29.access-token"); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := string(req.Header.Peek("Authorization")); got != "Bearer ya29.access-token" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := string(req.URI().QueryArgs().Peek("key")); got != "" {
		t.Fatalf("key query parameter = %q, want empty for OAuth", got)
	}
}

func TestSetupRequestHeaderUsesQueryKeyForAPIKey(t *testing.T) {
	adaptor := &GeminiAdaptor{}
	adaptor.Init(&db.Channel{Endpoint: "https://example.test"}, &db.Account{CredType: "api_key"})
	adaptor.SetRequestParams("gemini-2.5-pro", false)

	var req fasthttp.Request
	if err := adaptor.SetupRequestHeader(&req, "AIza-test-key"); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := string(req.Header.Peek("Authorization")); got != "" {
		t.Fatalf("Authorization = %q, want empty for API key", got)
	}
	if got := string(req.URI().QueryArgs().Peek("key")); got != "AIza-test-key" {
		t.Fatalf("key query parameter = %q, want API key", got)
	}
}

func TestParseUsageFullCapturesCodeAssistEnvelopeCachedContent(t *testing.T) {
	adaptor := &GeminiAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"response":{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"cachedContentTokenCount":7}}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 3 || usage.CacheReadInputTokens != 7 {
		t.Fatalf("usage = %#v, want prompt=12 completion=3 cache_read=7", usage)
	}
}
