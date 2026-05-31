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
			if _, ok := converter.(*irStreamConverter); !ok {
				t.Fatalf("NewConverter(%s, %s) = %T, want *irStreamConverter", upstream, client, converter)
			}
		}
	}
}
