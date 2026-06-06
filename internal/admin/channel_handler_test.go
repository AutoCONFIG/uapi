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
	valid := []string{"", "standard"}
	for _, format := range valid {
		if !isAPIKeyAPIFormat(format) {
			t.Fatalf("expected %q to be API Key format", format)
		}
	}
	invalid := []string{"responses", "codex", "claude_code", "gemini_code", "antigravity", "chatgpt_reverse"}
	for _, format := range invalid {
		if isAPIKeyAPIFormat(format) {
			t.Fatalf("expected %q not to be API Key format", format)
		}
	}
}
