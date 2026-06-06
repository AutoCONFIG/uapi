package upstreamconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

func TestCachePassthroughPolicyDefaultsByUpstreamFormat(t *testing.T) {
	tests := []struct {
		name string
		fmt  provider.Format
		want CachePassthroughPolicy
	}{
		{name: "openai chat", fmt: provider.FormatOpenAIChatCompletions, want: CachePassthroughPolicy{Enabled: true, PromptCacheKey: true, SynthesizePromptCacheKey: true, DropTrailingCacheControl: true}},
		{name: "responses", fmt: provider.FormatOpenAIResponses, want: CachePassthroughPolicy{Enabled: true, PromptCacheKey: true, SynthesizePromptCacheKey: true}},
		{name: "anthropic", fmt: provider.FormatAnthropic, want: CachePassthroughPolicy{Enabled: true, CacheControl: true}},
		{name: "gemini", fmt: provider.FormatGemini, want: CachePassthroughPolicy{Enabled: true, CachedContent: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CachePassthroughPolicyForChannel(nil, tt.fmt); got != tt.want {
				t.Fatalf("policy = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCachePassthroughPolicyCanBeDisabledByChannelSettings(t *testing.T) {
	ch := &db.Channel{Settings: `{"cache_passthrough":false}`}
	body := []byte(`{"model":"m","prompt_cache_key":"key","cachedContent":"cachedContents/1","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(ch, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	for _, forbidden := range []string{`prompt_cache_key`, `cachedContent`, `cache_control`} {
		if strings.Contains(string(got), forbidden) {
			t.Fatalf("cache field %s was not stripped: %s", forbidden, got)
		}
	}
}

func TestCachePassthroughSynthesizesPromptCacheKeyFromCacheControl(t *testing.T) {
	body := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if !strings.Contains(string(got), `"prompt_cache_key":"uapi-cache-`) {
		t.Fatalf("prompt_cache_key was not synthesized: %s", got)
	}
	if strings.Contains(string(got), `"cache_control"`) {
		t.Fatalf("cache_control should be stripped for OpenAI Chat upstream by default: %s", got)
	}

	got2, _, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy second call: %v", err)
	}
	if string(got) != string(got2) {
		t.Fatalf("synthesized prompt_cache_key should be stable:\n%s\n%s", got, got2)
	}
}

func TestCachePassthroughDropsTrailingUserCacheControlForOpenAIChat(t *testing.T) {
	body := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"volatile","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	text := string(got)
	if strings.Contains(text, `"cache_control"`) {
		t.Fatalf("cache_control should be stripped for OpenAI Chat upstream by default: %s", got)
	}
	if !strings.Contains(text, `"prompt_cache_key":"uapi-cache-`) {
		t.Fatalf("prompt_cache_key was not synthesized: %s", got)
	}
	if strings.Contains(text, `"text":"volatile","cache_control"`) {
		t.Fatalf("trailing user cache_control was not stripped: %s", got)
	}
}

func TestCachePassthroughPromptCacheKeyIgnoresVolatileTrailingMarkerWhenStableMarkerExists(t *testing.T) {
	body1 := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"volatile one","cache_control":{"type":"ephemeral"}}]}]}`)
	body2 := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"volatile two","cache_control":{"type":"ephemeral"}}]}]}`)

	got1, _, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body1)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy body1: %v", err)
	}
	got2, _, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body2)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy body2: %v", err)
	}
	key1 := promptCacheKeyFromBody(t, got1)
	key2 := promptCacheKeyFromBody(t, got2)
	if key1 == "" || key2 == "" {
		t.Fatalf("prompt_cache_key missing:\n%s\n%s", got1, got2)
	}
	if key1 != key2 {
		t.Fatalf("stable marker should produce stable key, got %q and %q", key1, key2)
	}
}

func TestCachePassthroughPromptCacheKeyChangesForOnlyTrailingMarker(t *testing.T) {
	body1 := []byte(`{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"volatile one","cache_control":{"type":"ephemeral"}}]}]}`)
	body2 := []byte(`{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"volatile two","cache_control":{"type":"ephemeral"}}]}]}`)

	got1, _, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body1)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy body1: %v", err)
	}
	got2, _, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body2)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy body2: %v", err)
	}
	key1 := promptCacheKeyFromBody(t, got1)
	key2 := promptCacheKeyFromBody(t, got2)
	if key1 == "" || key2 == "" {
		t.Fatalf("prompt_cache_key missing:\n%s\n%s", got1, got2)
	}
	if key1 == key2 {
		t.Fatalf("only trailing marker should not fake a stable cache key: %q", key1)
	}
}

func TestCachePassthroughDropsTrailingToolCacheControlForOpenAIChat(t *testing.T) {
	body := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"tool","tool_call_id":"call_1","content":"volatile","cache_control":{"type":"ephemeral"}}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	text := string(got)
	if strings.Contains(text, `"cache_control"`) {
		t.Fatalf("cache_control should be stripped for OpenAI Chat upstream by default: %s", got)
	}
	if strings.Contains(text, `"role":"tool","cache_control"`) || strings.Contains(text, `"cache_control":{"type":"ephemeral"},"content":"volatile"`) {
		t.Fatalf("trailing tool cache_control was not stripped: %s", got)
	}
}

func promptCacheKeyFromBody(t *testing.T, body []byte) string {
	t.Helper()
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("unmarshal body: %v\n%s", err, body)
	}
	key, _ := root["prompt_cache_key"].(string)
	return key
}

func TestCachePassthroughCanKeepTrailingUserCacheControl(t *testing.T) {
	ch := &db.Channel{Settings: `{"cache_passthrough":{"cache_control":true,"drop_trailing_cache_control":false}}`}
	body := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"volatile","cache_control":{"type":"ephemeral"}}]}]}`)
	got, _, err := ApplyCachePassthroughPolicy(ch, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if strings.Count(string(got), `"cache_control"`) != 2 {
		t.Fatalf("trailing cache_control should remain when disabled: %s", got)
	}
}

func TestCachePassthroughSynthesizesResponsesPromptCacheKeyAndStripsCacheControl(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stable","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(nil, provider.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	text := string(got)
	if !strings.Contains(text, `"prompt_cache_key":"uapi-cache-`) {
		t.Fatalf("prompt_cache_key was not synthesized: %s", got)
	}
	if strings.Contains(text, `"cache_control"`) {
		t.Fatalf("Responses upstream should not receive content cache_control: %s", got)
	}
}

func TestCachePassthroughCanDisablePromptCacheKeySynthesis(t *testing.T) {
	ch := &db.Channel{Settings: `{"cache_passthrough":{"synthesize_prompt_cache_key":false}}`}
	body := []byte(`{"model":"glm-5.1","messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(ch, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if strings.Contains(string(got), `"prompt_cache_key"`) {
		t.Fatalf("prompt_cache_key should not be synthesized: %s", got)
	}
	if strings.Contains(string(got), `"cache_control"`) {
		t.Fatalf("cache_control should be stripped by default: %s", got)
	}
}

func TestCachePassthroughPolicyObjectOverride(t *testing.T) {
	ch := &db.Channel{Settings: `{"cache_passthrough":{"cache_control":false}}`}
	body := []byte(`{"model":"m","prompt_cache_key":"key","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	got, changed, err := ApplyCachePassthroughPolicy(ch, provider.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ApplyCachePassthroughPolicy: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if !strings.Contains(string(got), `prompt_cache_key`) {
		t.Fatalf("prompt_cache_key should remain: %s", got)
	}
	if strings.Contains(string(got), `cache_control`) {
		t.Fatalf("cache_control should be stripped: %s", got)
	}
}
