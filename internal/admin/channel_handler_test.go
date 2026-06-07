package admin

import "testing"

func TestValidChannelFormatForType(t *testing.T) {
	valid := []struct {
		typ    string
		format string
	}{
		{"openai", ""},
		{"openai", "standard"},
		{"openai", "responses"},
		{"openai", "codex"},
		{"gemini", ""},
		{"gemini", "standard"},
		{"gemini", "gemini_code"},
		{"anthropic", ""},
		{"anthropic", "standard"},
		{"anthropic", "claude_code"},
		{"antigravity", "antigravity"},
	}
	for _, tc := range valid {
		if !validChannelFormatForType(tc.typ, tc.format) {
			t.Fatalf("expected %s/%s to be valid", tc.typ, tc.format)
		}
	}

	invalid := []struct {
		typ    string
		format string
	}{
		{"gemini", "codex"},
		{"gemini", "claude_code"},
		{"openai", "gemini_code"},
		{"anthropic", "responses"},
		{"anthropic", "codex"},
		{"antigravity", "standard"},
		{"antigravity", "gemini_code"},
		{"unknown", "standard"},
	}
	for _, tc := range invalid {
		if validChannelFormatForType(tc.typ, tc.format) {
			t.Fatalf("expected %s/%s to be invalid", tc.typ, tc.format)
		}
	}
}

func TestIsAPIKeyAPIFormat(t *testing.T) {
	valid := []string{"", "standard", "responses"}
	for _, format := range valid {
		if !isAPIKeyAPIFormat(format) {
			t.Fatalf("expected %q to be API Key format", format)
		}
	}
	invalid := []string{"codex", "claude_code", "gemini_code", "antigravity", "chatgpt_reverse"}
	for _, format := range invalid {
		if isAPIKeyAPIFormat(format) {
			t.Fatalf("expected %q not to be API Key format", format)
		}
	}
}

func TestResolveChannelTypeAndAPIFormat(t *testing.T) {
	gemini := "gemini"
	responses := "responses"

	typ, format := resolveChannelTypeAndAPIFormat("openai", "responses", &gemini, nil)
	if typ != "gemini" || format != "standard" {
		t.Fatalf("expected openai/responses switching to gemini to use standard, got %s/%s", typ, format)
	}

	typ, format = resolveChannelTypeAndAPIFormat("openai", "responses", &gemini, &responses)
	if typ != "gemini" || format != "responses" {
		t.Fatalf("expected explicit api_format to be preserved for validation, got %s/%s", typ, format)
	}
}

func TestDefaultAffinityTTLForOAuthChannelFormats(t *testing.T) {
	for _, format := range []string{"codex", "gemini_code", "claude_code", "antigravity"} {
		if got := defaultAffinityTTLForChannel("openai", format); got != DefaultOAuthChannelAffinityTTL {
			t.Fatalf("defaultAffinityTTLForChannel(%q) = %d, want %d", format, got, DefaultOAuthChannelAffinityTTL)
		}
	}
	for _, format := range []string{"", "standard", "responses", "chatgpt_reverse"} {
		if got := defaultAffinityTTLForChannel("openai", format); got != 0 {
			t.Fatalf("defaultAffinityTTLForChannel(%q) = %d, want 0", format, got)
		}
	}
}

func TestAffinityTTLOrDefaultPreservesExplicitZero(t *testing.T) {
	zero := 0
	if got := affinityTTLOrDefault(&zero, "openai", "codex"); got != 0 {
		t.Fatalf("explicit zero affinity ttl = %d, want 0", got)
	}
	if got := affinityTTLOrDefault(nil, "openai", "codex"); got != DefaultOAuthChannelAffinityTTL {
		t.Fatalf("missing affinity ttl = %d, want default", got)
	}
}
