package stream

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func TestOpenAIChatResponsesStreamsUseIRBridge(t *testing.T) {
	tests := []struct {
		name     string
		upstream convert.Format
		client   convert.Format
	}{
		{name: "chat to responses", upstream: convert.FormatOpenAIChatCompletions, client: convert.FormatOpenAIResponses},
		{name: "responses to chat", upstream: convert.FormatOpenAIResponses, client: convert.FormatOpenAIChatCompletions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := NewConverter(tt.upstream, tt.client)
			if _, ok := converter.(*irStreamConverter); !ok {
				t.Fatalf("NewConverter(%s, %s) = %T, want *irStreamConverter", tt.upstream, tt.client, converter)
			}
		})
	}
}
