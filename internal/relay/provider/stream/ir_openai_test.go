package stream

import (
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func TestProtocolStreamsUseIRConversion(t *testing.T) {
	formats := []convert.Format{
		convert.FormatOpenAIChatCompletions,
		convert.FormatOpenAIResponses,
		convert.FormatCodexResponses,
		convert.FormatAnthropic,
		convert.FormatClaudeCode,
		convert.FormatGemini,
		convert.FormatGeminiCode,
		convert.FormatGeminiCLI,
		convert.FormatAntigravity,
	}
	for _, upstream := range formats {
		for _, client := range formats {
			if upstream == client {
				continue
			}
			converter := NewConverter(upstream, client)
			if sameStreamFamily(upstream, client) {
				if converter != nil {
					t.Fatalf("NewConverter(%s, %s) = %T, want nil passthrough", upstream, client, converter)
				}
				continue
			}
			if _, ok := converter.(*irStreamConverter); !ok {
				t.Fatalf("NewConverter(%s, %s) = %T, want *irStreamConverter", upstream, client, converter)
			}
		}
	}
}

func TestWireCompatibleProtocolStreamsPassThrough(t *testing.T) {
	tests := []struct {
		upstream convert.Format
		client   convert.Format
	}{
		{convert.FormatCodexResponses, convert.FormatOpenAIResponses},
		{convert.FormatOpenAIResponses, convert.FormatCodexResponses},
		{convert.FormatClaudeCode, convert.FormatAnthropic},
		{convert.FormatAnthropic, convert.FormatClaudeCode},
		{convert.FormatGeminiCode, convert.FormatGemini},
		{convert.FormatGeminiCLI, convert.FormatAntigravity},
	}
	for _, tt := range tests {
		if converter := NewConverter(tt.upstream, tt.client); converter != nil {
			t.Fatalf("NewConverter(%s, %s) = %T, want nil passthrough", tt.upstream, tt.client, converter)
		}
	}
}

func TestResponsesToAnthropicStreamPreservesCacheUsage(t *testing.T) {
	converter := NewConverter(convert.FormatCodexResponses, convert.FormatAnthropic)
	if converter == nil {
		t.Fatal("NewConverter returned nil")
	}

	created := []byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5","status":"in_progress"}}` + "\n\n")
	completed := []byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.5","status":"completed","usage":{"input_tokens":100,"output_tokens":7,"total_tokens":107,"input_tokens_details":{"cached_tokens":64}}}}` + "\n\n")

	var out []byte
	out = append(out, converter.Convert(created)...)
	out = append(out, converter.Convert(completed)...)

	text := string(out)
	if !strings.Contains(text, `"input_tokens":100`) {
		t.Fatalf("Anthropic stream missing input_tokens: %s", text)
	}
	if !strings.Contains(text, `"output_tokens":7`) {
		t.Fatalf("Anthropic stream missing output_tokens: %s", text)
	}
	if !strings.Contains(text, `"cache_read_input_tokens":64`) {
		t.Fatalf("Anthropic stream missing cache_read_input_tokens: %s", text)
	}
}
