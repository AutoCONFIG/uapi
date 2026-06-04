package stream

import (
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
