package oauthprovider

import (
	"strings"
	"testing"
)

func TestOAuthDefaultModelsIncludeHiddenAndAliasModels(t *testing.T) {
	tests := []struct {
		provider string
		models   []string
	}{
		{"codex", []string{"gpt-5.5-openai-compact", "gpt-5.3-codex-spark", "codex-auto-review"}},
		{"gemini", []string{"auto", "gemini-3.1-pro-preview-customtools", "gemma-4-31b-it"}},
		{"anthropic", []string{"sonnet", "opus[1m]", "opusplan"}},
	}

	for _, tc := range tests {
		provider, ok := Get(tc.provider)
		if !ok {
			t.Fatalf("%s provider was not registered", tc.provider)
		}
		for _, model := range tc.models {
			if !csvContains(provider.Spec().Models, model) {
				t.Fatalf("%s models = %q, want %s", tc.provider, provider.Spec().Models, model)
			}
		}
	}
}

func csvContains(raw, want string) bool {
	for _, item := range strings.Split(raw, ",") {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}
