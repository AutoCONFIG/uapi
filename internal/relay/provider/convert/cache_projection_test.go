package convert

import (
	"encoding/json"
	"testing"
)

func TestPromptCacheKeyProjectionMatrix(t *testing.T) {
	tests := []struct {
		name   string
		source Format
		target Format
		body   []byte
		want   string
	}{
		{
			name:   "anthropic metadata user session to openai chat",
			source: FormatAnthropic,
			target: FormatOpenAIChatCompletions,
			body:   []byte(`{"model":"glm-5.1","max_tokens":8,"metadata":{"user_id":"{\"session_id\":\"sess_anthropic\"}"},"messages":[{"role":"user","content":"hi"}]}`),
			want:   "sess_anthropic",
		},
		{
			name:   "anthropic metadata user session to responses",
			source: FormatAnthropic,
			target: FormatOpenAIResponses,
			body:   []byte(`{"model":"glm-5.1","max_tokens":8,"metadata":{"user_id":"{\"session_id\":\"sess_anthropic\"}"},"messages":[{"role":"user","content":"hi"}]}`),
			want:   "sess_anthropic",
		},
		{
			name:   "gemini session id to openai chat",
			source: FormatGemini,
			target: FormatOpenAIChatCompletions,
			body:   []byte(`{"model":"glm-5.1","session_id":"sess_gemini","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
			want:   "sess_gemini",
		},
		{
			name:   "gemini session id to responses",
			source: FormatGemini,
			target: FormatOpenAIResponses,
			body:   []byte(`{"model":"glm-5.1","session_id":"sess_gemini","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
			want:   "sess_gemini",
		},
		{
			name:   "openai chat prompt cache key to responses",
			source: FormatOpenAIChatCompletions,
			target: FormatOpenAIResponses,
			body:   []byte(`{"model":"glm-5.1","prompt_cache_key":"sess_chat","messages":[{"role":"user","content":"hi"}]}`),
			want:   "sess_chat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := ConvertRequest(tt.source, tt.target, tt.body)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(converted, &got); err != nil {
				t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
			}
			if got["prompt_cache_key"] != tt.want {
				t.Fatalf("prompt_cache_key = %#v, want %q; body=%s", got["prompt_cache_key"], tt.want, converted)
			}
			if tt.target == FormatOpenAIChatCompletions {
				if _, ok := got["metadata"]; ok {
					t.Fatalf("source metadata leaked into OpenAI Chat: %s", converted)
				}
			}
		})
	}
}
