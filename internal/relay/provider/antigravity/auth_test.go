package antigravity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/valyala/fasthttp"
)

func TestAntigravityUserAgentUsesCurrentFallback(t *testing.T) {
	ua := AntigravityUserAgent()
	if ua != "antigravity/2.0.1 darwin/arm64" {
		t.Fatalf("AntigravityUserAgent() = %q", ua)
	}
	if got := LoadCodeAssistUserAgent(); !strings.HasPrefix(got, ua+" ") || !strings.Contains(got, NodeAPIClientUA) {
		t.Fatalf("LoadCodeAssistUserAgent() = %q, want %q plus node client", got, ua)
	}
}

func TestAntigravityAdaptorUsesNativeURLAndHeaders(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", Endpoint: "https://antigravity.example"}, &db.Account{CredType: "oauth_token"})

	gotURL, err := adaptor.GetRequestURL("/v1/chat/completions")
	if err != nil {
		t.Fatalf("GetRequestURL: %v", err)
	}
	if gotURL != "https://antigravity.example/v1internal:generateContent" {
		t.Fatalf("GetRequestURL = %q", gotURL)
	}

	var req fasthttp.Request
	if err := adaptor.SetupRequestHeader(&req, "ag-token"); err != nil {
		t.Fatalf("SetupRequestHeader: %v", err)
	}
	wants := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer ag-token",
		"User-Agent":    RequestUserAgent(),
	}
	for header, want := range wants {
		if got := string(req.Header.Peek(header)); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestParseUsageFullCapturesGeminiCachedContent(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"usageMetadata":{"promptTokenCount":22,"candidatesTokenCount":4,"cachedContentTokenCount":9}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 22 || usage.CompletionTokens != 4 || usage.CacheReadInputTokens != 9 {
		t.Fatalf("usage = %#v, want prompt=22 completion=4 cache_read=9", usage)
	}
}

func TestParseUsageFullCapturesAntigravityEnvelopeCachedContent(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"response":{"usageMetadata":{"promptTokenCount":31,"candidatesTokenCount":6,"cachedContentTokenCount":12}}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 31 || usage.CompletionTokens != 6 || usage.CacheReadInputTokens != 12 {
		t.Fatalf("usage = %#v, want prompt=31 completion=6 cache_read=12", usage)
	}
}

func TestAntigravityThinkingLevelBecomesNestedThinkingBudget(t *testing.T) {
	maxTokens := 20000
	adaptor := &AntigravityAdaptor{}
	body, err := adaptor.FromIR(&relayir.Request{
		Model: "claude-sonnet-4-6",
		Turns: []relayir.Turn{{
			Role: relayir.RoleUser,
			Items: []relayir.Item{{
				Kind: relayir.ItemText,
				Text: &relayir.Text{Text: "hi"},
			}},
		}},
		Generation: relayir.GenerationConfig{
			MaxTokens: &maxTokens,
			Thinking: json.RawMessage(`{
				"thinkingLevel":"MEDIUM",
				"includeThoughts":true
			}`),
		},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	request := envelope["request"].(map[string]interface{})
	generationConfig := request["generationConfig"].(map[string]interface{})
	if _, ok := generationConfig["thinkingBudget"]; ok {
		t.Fatalf("thinkingBudget must not be emitted at generationConfig top level: %s", body)
	}
	thinkingConfig := generationConfig["thinkingConfig"].(map[string]interface{})
	if _, ok := thinkingConfig["thinkingLevel"]; ok {
		t.Fatalf("thinkingLevel should be removed for Antigravity v1internal: %s", body)
	}
	if got := int(thinkingConfig["thinkingBudget"].(float64)); got != 10000 {
		t.Fatalf("thinkingBudget = %d, want 10000; body=%s", got, body)
	}
}

func TestAntigravityThinkingNoneEmitsZeroBudget(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	body, err := adaptor.FromIR(&relayir.Request{
		Model: "claude-sonnet-4-6",
		Turns: []relayir.Turn{{
			Role: relayir.RoleUser,
			Items: []relayir.Item{{
				Kind: relayir.ItemText,
				Text: &relayir.Text{Text: "hi"},
			}},
		}},
		Generation: relayir.GenerationConfig{
			Thinking: json.RawMessage(`{"thinkingLevel":"NONE"}`),
		},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	request := envelope["request"].(map[string]interface{})
	generationConfig := request["generationConfig"].(map[string]interface{})
	thinkingConfig := generationConfig["thinkingConfig"].(map[string]interface{})
	if got := int(thinkingConfig["thinkingBudget"].(float64)); got != 0 {
		t.Fatalf("thinkingBudget = %d, want 0; body=%s", got, body)
	}
}

func TestAntigravityDetectsGoogleSearchFunctionDeclarationConflict(t *testing.T) {
	request := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"googleSearch": map[string]interface{}{}},
			map[string]interface{}{"functionDeclarations": []interface{}{map[string]interface{}{"name": "lookup"}}},
		},
	}
	if !hasGoogleSearch(request) || !hasFunctionDeclarationsInRequest(request) {
		t.Fatalf("expected googleSearch/functionDeclarations conflict helpers to detect request: %#v", request)
	}
}

func TestAntigravityFromIRRejectsGoogleSearchFunctionDeclarationConflict(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	_, err := adaptor.FromIR(&relayir.Request{
		Model: "gemini-3-pro",
		Turns: []relayir.Turn{{
			Role: relayir.RoleUser,
			Items: []relayir.Item{{
				Kind: relayir.ItemText,
				Text: &relayir.Text{Text: "hi"},
			}},
		}},
		Native: relayir.NativeEnvelope{
			Fields: map[string]json.RawMessage{
				"tools": json.RawMessage(`[{"googleSearch":{}},{"functionDeclarations":[{"name":"lookup","parameters":{"type":"OBJECT"}}]}]`),
			},
		},
	})
	if err == nil {
		t.Fatalf("FromIR returned nil error, want googleSearch/functionDeclarations conflict")
	}
	if !strings.Contains(err.Error(), "googleSearch and functionDeclarations cannot coexist") {
		t.Fatalf("error = %q", err.Error())
	}
}
