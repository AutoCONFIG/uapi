package relay

import (
	"encoding/json"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

func TestNormalizeErrorResponseFormatsByClientFamily(t *testing.T) {
	cases := []struct {
		name         string
		clientFormat provider.Format
		body         []byte
		status       int
		assert       func(t *testing.T, got map[string]interface{})
	}{
		{
			name:         "openai",
			clientFormat: provider.FormatOpenAIChatCompletions,
			body:         []byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`),
			status:       fasthttp.StatusBadRequest,
			assert: func(t *testing.T, got map[string]interface{}) {
				errObj := got["error"].(map[string]interface{})
				if errObj["message"] != "bad request" || errObj["type"] != "relay_error" {
					t.Fatalf("unexpected OpenAI error: %#v", got)
				}
			},
		},
		{
			name:         "anthropic",
			clientFormat: provider.FormatAnthropic,
			body:         []byte(`{"error":{"message":"quota exceeded"}}`),
			status:       fasthttp.StatusTooManyRequests,
			assert: func(t *testing.T, got map[string]interface{}) {
				if got["type"] != "error" {
					t.Fatalf("unexpected Anthropic top-level type: %#v", got)
				}
				errObj := got["error"].(map[string]interface{})
				if errObj["message"] != "quota exceeded" || errObj["type"] != "api_error" {
					t.Fatalf("unexpected Anthropic error: %#v", got)
				}
			},
		},
		{
			name:         "gemini",
			clientFormat: provider.FormatGemini,
			body:         []byte(`{"error":{"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`),
			status:       fasthttp.StatusTooManyRequests,
			assert: func(t *testing.T, got map[string]interface{}) {
				errObj := got["error"].(map[string]interface{})
				if errObj["message"] != "quota exceeded" || errObj["status"] != "RESOURCE_EXHAUSTED" || int(errObj["code"].(float64)) != fasthttp.StatusTooManyRequests {
					t.Fatalf("unexpected Gemini error: %#v", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]interface{}
			if err := json.Unmarshal(normalizeErrorResponse(tc.body, tc.clientFormat, tc.status), &got); err != nil {
				t.Fatalf("normalized error is not valid JSON: %v", err)
			}
			tc.assert(t, got)
		})
	}
}

func TestNormalizeErrorResponseRewritesGenericUpstream413(t *testing.T) {
	got := normalizeErrorResponse([]byte(`{"error":{"message":"api_error"}}`), provider.FormatGemini, fasthttp.StatusRequestEntityTooLarge)

	var decoded struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("normalized error is not valid JSON: %v", err)
	}
	if decoded.Error.Code != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want %d", decoded.Error.Code, fasthttp.StatusRequestEntityTooLarge)
	}
	if decoded.Error.Message != "upstream returned HTTP 413: request body too large" {
		t.Fatalf("message = %q", decoded.Error.Message)
	}
}
