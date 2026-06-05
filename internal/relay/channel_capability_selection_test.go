package relay

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/channelcap"
	"github.com/AutoCONFIG/uapi/internal/db"
)

func TestChannelCandidatesSkipChatGPTReverseForUnsupportedFeatures(t *testing.T) {
	channels := []db.Channel{
		{Name: "reverse", Type: "openai", APIFormat: "chatgpt_reverse", Models: "gpt-5.5", Priority: 100, Weight: 100},
		{Name: "standard", Type: "openai", APIFormat: "standard", Models: "gpt-5.5", Priority: 10, Weight: 100},
	}

	candidates := channelCandidatesForModelAndCapability(channels, "gpt-5.5", []channelcap.Request{{
		Kind:     channelcap.KindChatCompletion,
		HasTools: true,
	}}, nil)

	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	if candidates[0].Name != "standard" {
		t.Fatalf("candidate = %q, want standard", candidates[0].Name)
	}
}
