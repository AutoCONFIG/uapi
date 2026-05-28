package convert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/stream"
)

func TestStreamConvertersAcceptSSEAndEmitValidJSON(t *testing.T) {
	tests := []struct {
		name     string
		upstream convert.Format
		client   convert.Format
		input    string
		wantType string
	}{
		{
			name:     "responses to chat",
			upstream: convert.FormatOpenAIResponses,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"type":"response.created","id":"resp_1","model":"gpt-5"}` + "\n\n",
		},
		{
			name:     "chat to responses",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatOpenAIResponses,
			input:    `data: {"id":"chatcmpl_1","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n",
			wantType: "response.created",
		},
		{
			name:     "anthropic to chat",
			upstream: convert.FormatAnthropic,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n",
		},
		{
			name:     "chat to anthropic",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatAnthropic,
			input:    `data: {"id":"chatcmpl_1","model":"claude","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n",
			wantType: "message_start",
		},
		{
			name:     "gemini to chat",
			upstream: convert.FormatGemini,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n",
		},
		{
			name:     "chat to gemini",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatGemini,
			input:    `data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n",
			wantType: "generateContentStream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := stream.NewConverter(tt.upstream, tt.client)
			if converter == nil {
				t.Fatalf("missing stream converter for %s -> %s", tt.upstream, tt.client)
			}
			out := converter.Convert([]byte(tt.input))
			if len(out) == 0 {
				t.Fatalf("converter emitted no output")
			}
			payload := firstSSEPayload(t, out)
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				t.Fatalf("converter emitted invalid JSON: %v\n%s", err, out)
			}
			if tt.wantType != "" {
				if typ, _ := obj["type"].(string); typ != tt.wantType {
					if method, _ := obj["method"].(string); method != tt.wantType {
						t.Fatalf("unexpected event type/method: %#v", obj)
					}
				}
			}
		})
	}
}

func TestGeminiStreamIDsAreNotConstant(t *testing.T) {
	input := []byte(`data: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n")
	first := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions).Convert(input)
	second := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions).Convert(input)
	firstID := jsonStringField(t, firstSSEPayload(t, first), "id")
	secondID := jsonStringField(t, firstSSEPayload(t, second), "id")
	if firstID == "" || secondID == "" {
		t.Fatalf("missing generated IDs: %q %q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("gemini stream converter generated constant ID %q", firstID)
	}
}

func TestStreamConvertersUseClientToProviderDirection(t *testing.T) {
	input := []byte(`data: {"id":"chatcmpl_1","model":"model","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n")
	tests := []struct {
		name     string
		client   convert.Format
		wantType string
	}{
		{name: "anthropic", client: convert.FormatAnthropic, wantType: "content_block_delta"},
		{name: "gemini", client: convert.FormatGemini, wantType: "generateContentStream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, tt.client)
			if converter == nil {
				t.Fatalf("missing reverse converter")
			}
			out := converter.Convert(input)
			if len(out) == 0 {
				t.Fatalf("converter emitted no output")
			}
			payload := firstSSEPayload(t, out)
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				t.Fatalf("invalid JSON output: %v\n%s", err, out)
			}
			if typ, _ := obj["type"].(string); typ != tt.wantType {
				if method, _ := obj["method"].(string); method != tt.wantType {
					t.Fatalf("unexpected reverse event: %#v", obj)
				}
			}
		})
	}
}

func TestGeminiStreamConverterHandlesCodeAssistResponseWrapper(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions)
	out := converter.Convert([]byte("event: message\n" +
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\n" +
		"data: \"hi\"}]}}]}}\n\n"))
	if !strings.Contains(string(out), `"content":"hi"`) {
		t.Fatalf("CodeAssist response wrapper was not converted: %s", out)
	}
}

func firstSSEPayload(t *testing.T, event []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(event), "\n") {
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	t.Fatalf("missing data line in SSE event:\n%s", event)
	return ""
}

func jsonStringField(t *testing.T, payload, field string) string {
	t.Helper()
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	value, _ := obj[field].(string)
	return value
}
