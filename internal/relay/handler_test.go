package relay

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

func TestNormalizeErrorResponse_OpenAIFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"429"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChatCompletions, fasthttp.StatusInternalServerError)

	expected := `{"error":{"message":"Rate limit exceeded","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("OpenAI format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_AnthropicFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatAnthropic, fasthttp.StatusInternalServerError)

	expected := `{"error":{"message":"invalid x-api-key","type":"api_error"},"type":"error"}`
	if string(result) != expected {
		t.Errorf("Anthropic format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_GeminiFormat(t *testing.T) {
	upstream := []byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatGemini, fasthttp.StatusTooManyRequests)

	expected := `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`
	if string(result) != expected {
		t.Errorf("Gemini format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_GeminiTopLevelMessage(t *testing.T) {
	upstream := []byte(`{"message":"API key not valid"}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChatCompletions, fasthttp.StatusInternalServerError)

	expected := `{"error":{"message":"API key not valid","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Gemini top-level message mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_DetailField(t *testing.T) {
	upstream := []byte(`{"detail":"Not Found"}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChatCompletions, fasthttp.StatusInternalServerError)

	expected := `{"error":{"message":"Not Found","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Detail field mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_UnparseableBody(t *testing.T) {
	upstream := []byte(`<html>error page</html>`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChatCompletions, fasthttp.StatusInternalServerError)

	expected := `{"error":{"message":"upstream error","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Unparseable body mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_OpenAIRespFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"model not found"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIResponses, fasthttp.StatusInternalServerError)

	// OpenAI Responses format should fall through to default (OpenAI) format
	expected := `{"error":{"message":"model not found","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("OpenAI Responses format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestStripProviderInfo(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"org-abc123: something went wrong", "abc123: something went wrong"},
		{"req_12345: rate limited", "12345: rate limited"},
		{"normal error message", "normal error message"},
		{"", ""},
	}

	for _, tt := range tests {
		result := stripProviderInfo(tt.input)
		if result != tt.expected {
			t.Errorf("stripProviderInfo(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestClientIPCandidatesIncludesProxyHeaders(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Real-IP", "203.0.113.10")
	ctx.Request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.2")

	// Without trusted proxies, only remote IP is returned
	got := clientIPCandidates(&ctx, nil)
	if len(got) != 1 {
		t.Fatalf("expected only remote IP without trusted proxies, got %#v", got)
	}

	// With trusted proxies matching remote IP, proxy headers are included
	got = clientIPCandidates(&ctx, []string{"0.0.0.0"})
	if len(got) < 3 {
		t.Fatalf("expected remote IP plus proxy headers, got %#v", got)
	}
	if got[1] != "203.0.113.10" {
		t.Fatalf("X-Real-IP candidate mismatch: %#v", got)
	}
	if got[2] != "198.51.100.8" {
		t.Fatalf("X-Forwarded-For first-hop candidate mismatch: %#v", got)
	}
}

func TestPermissionForRequestImages(t *testing.T) {
	if got := permissionForRequest("/v1/images/generations", provider.FormatOpenAIResponses); got != "images" {
		t.Fatalf("image permission = %q, want images", got)
	}
}

func TestPermissionForRequestFormatFallback(t *testing.T) {
	if got := permissionForRequest("/v1/responses", provider.FormatOpenAIResponses); got != "responses" {
		t.Fatalf("responses permission = %q, want responses", got)
	}
}

func TestConvertSSEBufferWithConverterJoinsMultiLineDataEvent(t *testing.T) {
	body := []byte("data: {\"a\":1,\n" +
		"data: \"b\":2}\n\n")
	var gotInput string
	out := convertSSEBufferWithConverter(body, func(line []byte) []byte {
		gotInput = string(line)
		return []byte("data: {\"choices\":[]}\n\n")
	})
	if gotInput != "data: {\"a\":1,\n\"b\":2}\n\n" {
		t.Fatalf("converter input mismatch: %q", gotInput)
	}
	if string(out) != "data: {\"choices\":[]}\n\ndata: [DONE]\n\n" {
		t.Fatalf("converted output mismatch: %q", out)
	}
}

func TestStreamToNonStreamPreservesErrorChunk(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","object":"error","error":{"message":"upstream failed","type":"conversion_error"}}` + "\n\n")
	out := string(StreamToNonStream(body))
	if out != `{"error":{"message":"upstream failed","type":"conversion_error"},"id":"chatcmpl-test","object":"error"}` {
		t.Fatalf("error chunk must not be converted to empty chat completion: %s", out)
	}
}

func TestCompactLogBodyRedactsBearerTokenValue(t *testing.T) {
	got := compactLogBody([]byte(`{"authorization":"Bearer ya29.secret","message":"failed"}`))
	if got != `{"authorization":"[redacted]","message":"failed"}` {
		t.Fatalf("redacted body mismatch: %q", got)
	}
}
