package antigravity_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
)

func TestPublicModelCatalogHasExpectedModels(t *testing.T) {
	want := []string{
		"gemini-3.5-flash",
		"gemini-3.1-pro",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b",
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
		"gemini-3.5-flash":         "gemini-3-flash-agent",
		"gemini-3.5-flash-high":    "gemini-3-flash-agent",
		"gemini-3.5-flash-medium":  "gemini-3.5-flash-medium",
		"gemini-3.5-flash-low":     "gemini-3.5-flash-low",
		"gemini-3.1-pro":           "gemini-pro-agent",
		"gemini-3.1-pro-high":      "gemini-pro-agent",
		"gemini-3.1-pro-low":       "gemini-3.1-pro-low",
		"claude-sonnet-4-6":        "claude-sonnet-4-6",
		"claude-opus-4-6-thinking": "claude-opus-4-6-thinking",
		"gpt-oss-120b":             "gpt-oss-120b-medium",
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
		"gemini-3.5-flash",
		"gemini-3.1-pro",
		"claude-sonnet-4-6",
		"claude-opus-4-6-thinking",
		"gpt-oss-120b",
		"nano-banana-2",
	}

	if got := antigravity.NormalizeAvailableModels(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAvailableModels() = %#v, want %#v", got, want)
	}
}

func TestDisplayNameReturnsFriendlyQuotaLabels(t *testing.T) {
	tests := map[string]string{
		"gemini-3-flash-agent":       "Gemini 3.5 Flash",
		"gemini-3.5-flash-high":      "Gemini 3.5 Flash",
		"gemini-3.5-flash-low":       "Gemini 3.5 Flash",
		"gemini-pro-agent":           "Gemini 3.1 Pro",
		"claude-sonnet-4-6-thinking": "Claude Sonnet 4.6 (Thinking)",
		"MODEL_PLACEHOLDER_M26":      "Claude Opus 4.6 (Thinking)",
		"gpt-oss-120b-medium":        "GPT-OSS 120B",
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

func TestUpstreamModelIDForEffortRoutesAntigravityTiers(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		effort      string
		requestSize string
		want        string
	}{
		{name: "flash short defaults high", model: "gemini-3.5-flash", requestSize: "short", want: "gemini-3-flash-agent"},
		{name: "flash medium defaults medium", model: "gemini-3.5-flash", requestSize: "medium", want: "gemini-3.5-flash-medium"},
		{name: "flash long defaults low", model: "gemini-3.5-flash", requestSize: "long", want: "gemini-3.5-flash-low"},
		{name: "pro medium falls back high", model: "gemini-3.1-pro", requestSize: "medium", want: "gemini-pro-agent"},
		{name: "pro long defaults low", model: "gemini-3.1-pro", requestSize: "long", want: "gemini-3.1-pro-low"},
		{name: "explicit low overrides short", model: "gemini-3.1-pro", effort: "low", requestSize: "short", want: "gemini-3.1-pro-low"},
		{name: "explicit high overrides long", model: "gemini-3.1-pro", effort: "high", requestSize: "long", want: "gemini-pro-agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := antigravity.UpstreamModelIDForEffort(tt.model, tt.effort, tt.requestSize); got != tt.want {
				t.Fatalf("UpstreamModelIDForEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAntigravityTierFallbackOrder(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		current  string
		expected []string
	}{
		{name: "flash high falls through medium then low", model: "gemini-3.5-flash", current: "gemini-3-flash-agent", expected: []string{"gemini-3.5-flash-medium", "gemini-3.5-flash-low"}},
		{name: "flash medium tries high then low", model: "gemini-3.5-flash", current: "gemini-3.5-flash-medium", expected: []string{"gemini-3-flash-agent", "gemini-3.5-flash-low"}},
		{name: "pro low tries high without duplicate medium", model: "gemini-3.1-pro", current: "gemini-3.1-pro-low", expected: []string{"gemini-pro-agent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := antigravity.FallbackUpstreamModels(tt.model, tt.current); !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("FallbackUpstreamModels() = %#v, want %#v", got, tt.expected)
			}
		})
	}
}

func TestUpstreamModelIDForEffortCanLeaveModelUnchanged(t *testing.T) {
	if got := antigravity.UpstreamModelIDForEffortWithThresholds("gemini-3.5-flash", "high", "short", false); got != "gemini-3.5-flash" {
		t.Fatalf("disabled tier routing = %q, want original model", got)
	}
	if got := antigravity.UpstreamModelIDForEffortWithThresholds("gemini-3.5-flash-low", "", "short", false); got != "gemini-3.5-flash-low" {
		t.Fatalf("explicit tier model = %q, want explicit upstream", got)
	}
}
