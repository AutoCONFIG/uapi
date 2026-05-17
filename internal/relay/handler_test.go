package relay

import (
	"testing"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

func TestNormalizeErrorResponse_OpenAIFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"429"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChat)

	expected := `{"error":{"message":"Rate limit exceeded","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("OpenAI format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_AnthropicFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatAnthropic)

	expected := `{"error":{"message":"invalid x-api-key","type":"api_error"},"type":"error"}`
	if string(result) != expected {
		t.Errorf("Anthropic format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_GeminiFormat(t *testing.T) {
	upstream := []byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatGemini)

	expected := `{"error":{"code":500,"message":"Quota exceeded","status":"INTERNAL"}}`
	if string(result) != expected {
		t.Errorf("Gemini format mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_GeminiTopLevelMessage(t *testing.T) {
	upstream := []byte(`{"message":"API key not valid"}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChat)

	expected := `{"error":{"message":"API key not valid","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Gemini top-level message mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_DetailField(t *testing.T) {
	upstream := []byte(`{"detail":"Not Found"}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChat)

	expected := `{"error":{"message":"Not Found","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Detail field mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_UnparseableBody(t *testing.T) {
	upstream := []byte(`<html>error page</html>`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIChat)

	expected := `{"error":{"message":"upstream error","type":"relay_error"}}`
	if string(result) != expected {
		t.Errorf("Unparseable body mismatch:\ngot:      %s\nexpected: %s", result, expected)
	}
}

func TestNormalizeErrorResponse_OpenAIRespFormat(t *testing.T) {
	upstream := []byte(`{"error":{"message":"model not found"}}`)
	result := normalizeErrorResponse(upstream, provider.FormatOpenAIResp)

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
