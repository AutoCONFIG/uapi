package gemini

import (
	"strings"
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

func TestUnwrapCodeAssistSSEBufferUsesFullEvents(t *testing.T) {
	body := []byte("event: message\n" +
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\n" +
		"data: \"hi\"}]}}]}}\n\n")
	out := string(unwrapCodeAssistSSEBuffer(body))
	if !strings.Contains(out, `"candidates"`) || !strings.Contains(out, `"text":"hi"`) {
		t.Fatalf("CodeAssist SSE unwrap must normalize full events: %s", out)
	}
	if !strings.Contains(out, `"response"`) {
		t.Fatalf("CodeAssist wrapper should be preserved for Gemini stream converter: %s", out)
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
