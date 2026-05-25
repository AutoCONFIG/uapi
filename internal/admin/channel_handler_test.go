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
