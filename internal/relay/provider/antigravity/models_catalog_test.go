package antigravity_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
)

func TestPublicModelCatalogHasExpectedModels(t *testing.T) {
	want := []string{
		"gemini-3.5-flash-medium",
		"gemini-3.5-flash-high",
		"gemini-3.5-flash-low",
		"gemini-3.1-pro-low",
		"gemini-3.1-pro-high",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b-medium",
		"nano-banana-2",
	}

	if got := strings.Split(antigravity.PublicModelCSV(), ","); !reflect.DeepEqual(got, want) {
		t.Fatalf("PublicModelCSV() = %#v, want %#v", got, want)
	}
	if got := antigravity.PublicModels(); len(got) != len(want) {
		t.Fatalf("PublicModels() length = %d, want %d", len(got), len(want))
	}
}

func TestUpstreamModelIDMapsPublicIDsToAntigravityIDs(t *testing.T) {
	tests := map[string]string{
		"gemini-3.5-flash-medium":  "gemini-3-flash-agent",
		"gemini-3.5-flash-high":    "gemini-3-flash-agent",
		"gemini-3.5-flash-low":     "gemini-3.5-flash-low",
		"gemini-3.1-pro-high":      "gemini-pro-agent",
		"gemini-3.1-pro-low":       "gemini-3.1-pro-low",
		"claude-sonnet-4-6":        "claude-sonnet-4-6",
		"claude-opus-4-6-thinking": "claude-opus-4-6-thinking",
		"gpt-oss-120b-medium":      "gpt-oss-120b-medium",
		"nano-banana-2":            "gemini-3.1-flash-image",
		"gpt-image-1":              "gemini-3.1-flash-image",
	}

	for input, want := range tests {
		if got := antigravity.UpstreamModelID(input); got != want {
			t.Fatalf("UpstreamModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAvailableModelsUsesCanonicalPublicOrder(t *testing.T) {
	input := []string{
		"models/gemini-pro-agent",
		"gemini-3-flash-agent",
		"gemini-3.5-flash-low",
		"gemini-3.1-pro-low",
		"claude-sonnet-4-6-thinking",
		"claude-opus-4-6",
		"gpt-oss-120b-medium",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite",
		"chat_20706",
	}
	want := []string{
		"gemini-3.5-flash-medium",
		"gemini-3.5-flash-high",
		"gemini-3.5-flash-low",
		"gemini-3.1-pro-low",
		"gemini-3.1-pro-high",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b-medium",
		"nano-banana-2",
	}

	if got := antigravity.NormalizeAvailableModels(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAvailableModels() = %#v, want %#v", got, want)
	}
}

func TestDisplayNameReturnsFriendlyQuotaLabels(t *testing.T) {
	tests := map[string]string{
		"gemini-3-flash-agent":       "Gemini 3.5 Flash (Medium)",
		"gemini-3.5-flash-high":      "Gemini 3.5 Flash (High)",
		"gemini-3.5-flash-low":       "Gemini 3.5 Flash (Low)",
		"gemini-pro-agent":           "Gemini 3.1 Pro (High)",
		"claude-sonnet-4-6-thinking": "Claude Sonnet 4.6 (Thinking)",
		"MODEL_PLACEHOLDER_M26":      "Claude Opus 4.6 (Thinking)",
		"gpt-oss-120b-medium":        "GPT-OSS 120B (Medium)",
		"nano-banana-2":              "Nano Banana 2",
		"gemini-3.1-flash-image":     "Nano Banana 2",
		"gemini-3-pro-image-preview": "Nano Banana 2",
	}

	for input, want := range tests {
		if got := antigravity.DisplayName(input); got != want {
			t.Fatalf("DisplayName(%q) = %q, want %q", input, got, want)
		}
	}
}
