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

func TestGeminiCodeAssistSetupRequestHeaderUsesNativeContract(t *testing.T) {
	adaptor := &GeminiAdaptor{}
	adaptor.Init(&db.Channel{Type: "gemini", APIFormat: "gemini_code", Endpoint: "https://generativelanguage.googleapis.com"}, &db.Account{CredType: "oauth_token"})
	adaptor.SetRequestParams("gemini-2.5-pro", true)

	gotURL, err := adaptor.GetRequestURL("/v1beta/models/gemini-2.5-pro:streamGenerateContent")
	if err != nil {
		t.Fatalf("GetRequestURL: %v", err)
	}
	if gotURL != CodeAssistEndpoint+"/v1internal:generateContent" {
		t.Fatalf("GetRequestURL = %q", gotURL)
	}

	var req fasthttp.Request
	if err := adaptor.SetupRequestHeader(&req, "ya29.code-assist"); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := string(req.URI().FullURI()); got != CodeAssistEndpoint+"/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("request URI = %q", got)
	}
	if got := string(req.Header.Peek("Authorization")); got != "Bearer ya29.code-assist" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := string(req.Header.Peek("User-Agent")); got != GeminiCLIUserAgent("gemini-2.5-pro") {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := string(req.Header.Peek("Content-Type")); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := string(req.URI().QueryArgs().Peek("key")); got != "" {
		t.Fatalf("key query parameter = %q, want empty for Code Assist OAuth", got)
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

func TestParseUsageFullCapturesNativeGeminiCachedContent(t *testing.T) {
	adaptor := &GeminiAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"usageMetadata":{"promptTokenCount":14,"candidatesTokenCount":5,"totalTokenCount":19,"cachedContentTokenCount":8}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 14 || usage.CompletionTokens != 5 || usage.CacheReadInputTokens != 8 {
		t.Fatalf("usage = %#v, want prompt=14 completion=5 cache_read=8", usage)
	}
}
