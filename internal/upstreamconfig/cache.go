package upstreamconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

type CachePassthroughPolicy struct {
	Enabled                    bool `json:"enabled"`
	PromptCacheKey             bool `json:"prompt_cache_key"`
	SynthesizePromptCacheKey   bool `json:"synthesize_prompt_cache_key"`
	CacheControl               bool `json:"cache_control"`
	CachedContent              bool `json:"cached_content"`
	DropTrailingCacheControl   bool `json:"drop_trailing_cache_control"`
	DroppedTrailingCacheMarker bool `json:"dropped_trailing_cache_marker,omitempty"`
}

func CachePassthroughPolicyForChannel(ch *db.Channel, upstreamFormat provider.Format) CachePassthroughPolicy {
	policy := defaultCachePassthroughPolicy(upstreamFormat)
	if ch == nil || ch.Settings == "" {
		return policy
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ch.Settings), &settings); err != nil {
		return policy
	}
	raw := settings["cache_passthrough"]
	if len(raw) == 0 {
		raw = settings["upstream_cache"]
	}
	if len(raw) == 0 {
		return policy
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err == nil {
		policy.Enabled = enabled
		if !enabled {
			policy.PromptCacheKey = false
			policy.SynthesizePromptCacheKey = false
			policy.CacheControl = false
			policy.CachedContent = false
		}
		return policy
	}
	var override struct {
		Enabled                  *bool `json:"enabled"`
		PromptCacheKey           *bool `json:"prompt_cache_key"`
		SynthesizePromptCacheKey *bool `json:"synthesize_prompt_cache_key"`
		CacheControl             *bool `json:"cache_control"`
		CachedContent            *bool `json:"cached_content"`
		DropTrailingCacheControl *bool `json:"drop_trailing_cache_control"`
	}
	if err := json.Unmarshal(raw, &override); err != nil {
		return policy
	}
	if override.Enabled != nil {
		policy.Enabled = *override.Enabled
	}
	if override.PromptCacheKey != nil {
		policy.PromptCacheKey = *override.PromptCacheKey
	}
	if override.SynthesizePromptCacheKey != nil {
		policy.SynthesizePromptCacheKey = *override.SynthesizePromptCacheKey
	}
	if override.CacheControl != nil {
		policy.CacheControl = *override.CacheControl
	}
	if override.CachedContent != nil {
		policy.CachedContent = *override.CachedContent
	}
	if override.DropTrailingCacheControl != nil {
		policy.DropTrailingCacheControl = *override.DropTrailingCacheControl
	}
	if !policy.Enabled {
		policy.PromptCacheKey = false
		policy.SynthesizePromptCacheKey = false
		policy.CacheControl = false
		policy.CachedContent = false
		policy.DropTrailingCacheControl = false
	}
	return policy
}

func ApplyCachePassthroughPolicy(ch *db.Channel, upstreamFormat provider.Format, body []byte) ([]byte, bool, error) {
	policy := CachePassthroughPolicyForChannel(ch, upstreamFormat)
	if policy.Enabled && policy.PromptCacheKey && !policy.SynthesizePromptCacheKey && policy.CacheControl && policy.CachedContent {
		return body, false, nil
	}
	var root interface{}
	if err := decodeJSONUseNumber(body, &root); err != nil {
		return body, false, err
	}
	changed := false
	if pruneTrailingCacheControl(root, policy) {
		changed = true
	}
	if synthesizePromptCacheKey(root, policy) {
		changed = true
	}
	if applyCachePassthroughPolicyValue(root, policy) {
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body, false, err
	}
	return out, true, nil
}

func defaultCachePassthroughPolicy(upstreamFormat provider.Format) CachePassthroughPolicy {
	policy := CachePassthroughPolicy{Enabled: true}
	switch upstreamFormat {
	case provider.FormatOpenAIChatCompletions:
		policy.PromptCacheKey = true
		policy.SynthesizePromptCacheKey = true
		policy.CacheControl = false
		policy.DropTrailingCacheControl = true
	case provider.FormatOpenAIResponses, provider.FormatCodexResponses:
		policy.PromptCacheKey = true
		policy.SynthesizePromptCacheKey = true
	case provider.FormatAnthropic, provider.FormatClaudeCode:
		policy.CacheControl = true
	case provider.FormatGemini, provider.FormatGeminiCode, provider.FormatGeminiCLI, provider.FormatAntigravity:
		policy.CachedContent = true
	}
	return policy
}

func pruneTrailingCacheControl(value interface{}, policy CachePassthroughPolicy) bool {
	if !policy.Enabled || !policy.CacheControl || !policy.DropTrailingCacheControl {
		return false
	}
	root, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	messages, ok := root["messages"].([]interface{})
	if !ok || len(messages) == 0 || countCacheControlMarkers(value) < 2 {
		return false
	}
	last, ok := messages[len(messages)-1].(map[string]interface{})
	if !ok {
		return false
	}
	return deleteCacheControlRecursive(last)
}

func countCacheControlMarkers(value interface{}) int {
	switch v := value.(type) {
	case map[string]interface{}:
		count := 0
		for key, child := range v {
			if key == "cache_control" {
				count++
			}
			count += countCacheControlMarkers(child)
		}
		return count
	case []interface{}:
		count := 0
		for _, child := range v {
			count += countCacheControlMarkers(child)
		}
		return count
	default:
		return 0
	}
}

func deleteCacheControlRecursive(value interface{}) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		changed := false
		if _, ok := v["cache_control"]; ok {
			delete(v, "cache_control")
			changed = true
		}
		for _, child := range v {
			if deleteCacheControlRecursive(child) {
				changed = true
			}
		}
		return changed
	case []interface{}:
		changed := false
		for _, child := range v {
			if deleteCacheControlRecursive(child) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func applyCachePassthroughPolicyValue(value interface{}, policy CachePassthroughPolicy) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		changed := false
		if !policy.PromptCacheKey {
			if _, ok := v["prompt_cache_key"]; ok {
				delete(v, "prompt_cache_key")
				changed = true
			}
		}
		if !policy.CachedContent {
			if _, ok := v["cachedContent"]; ok {
				delete(v, "cachedContent")
				changed = true
			}
		}
		if !policy.CacheControl {
			if _, ok := v["cache_control"]; ok {
				delete(v, "cache_control")
				changed = true
			}
		}
		for _, child := range v {
			if applyCachePassthroughPolicyValue(child, policy) {
				changed = true
			}
		}
		return changed
	case []interface{}:
		changed := false
		for _, child := range v {
			if applyCachePassthroughPolicyValue(child, policy) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func synthesizePromptCacheKey(value interface{}, policy CachePassthroughPolicy) bool {
	if !policy.Enabled || !policy.PromptCacheKey || !policy.SynthesizePromptCacheKey {
		return false
	}
	root, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	if _, exists := root["prompt_cache_key"]; exists {
		return false
	}
	marked := collectCacheControlPrefixJSON(root)
	if len(marked) == 0 {
		return false
	}
	h := sha256.New()
	if model, ok := root["model"].(string); ok {
		_, _ = h.Write([]byte(model))
	}
	for _, raw := range marked {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
	}
	sum := h.Sum(nil)
	root["prompt_cache_key"] = "uapi-cache-" + hex.EncodeToString(sum[:16])
	return true
}

type cacheControlPrefix struct {
	raw      []byte
	trailing bool
}

func collectCacheControlPrefixJSON(root map[string]interface{}) [][]byte {
	var prefixes []cacheControlPrefix
	collectMessageCacheControlPrefixes(root, &prefixes)
	collectArrayCacheControlPrefixes(root, "input", &prefixes)
	collectArrayCacheControlPrefixes(root, "tools", &prefixes)
	if len(prefixes) == 0 {
		collectCacheControlMarkedJSON(root, &prefixes)
	}
	stable := make([][]byte, 0, len(prefixes))
	trailing := make([][]byte, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.trailing {
			trailing = append(trailing, prefix.raw)
			continue
		}
		stable = append(stable, prefix.raw)
	}
	if len(stable) > 0 {
		return stable
	}
	return trailing
}

func collectMessageCacheControlPrefixes(root map[string]interface{}, out *[]cacheControlPrefix) {
	messages, ok := root["messages"].([]interface{})
	if !ok {
		return
	}
	for i, item := range messages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		trailing := i == len(messages)-1
		if _, ok := msg["cache_control"]; ok {
			if raw, ok := cacheControlPrefixRaw(root, "messages", i, -1); ok {
				*out = append(*out, cacheControlPrefix{raw: raw, trailing: trailing})
			}
		}
		content, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for j, part := range content {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if _, ok := partMap["cache_control"]; ok {
				if raw, ok := cacheControlPrefixRaw(root, "messages", i, j); ok {
					*out = append(*out, cacheControlPrefix{raw: raw, trailing: trailing})
				}
			}
		}
	}
}

func collectArrayCacheControlPrefixes(root map[string]interface{}, field string, out *[]cacheControlPrefix) {
	items, ok := root[field].([]interface{})
	if !ok {
		return
	}
	for i, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := itemMap["cache_control"]; ok {
			if raw, ok := cacheControlPrefixRaw(root, field, i, -1); ok {
				*out = append(*out, cacheControlPrefix{raw: raw, trailing: i == len(items)-1})
			}
		}
	}
}

func cacheControlPrefixRaw(root map[string]interface{}, field string, itemIdx, partIdx int) ([]byte, bool) {
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, false
	}
	var clone map[string]interface{}
	if err := decodeJSONUseNumber(raw, &clone); err != nil {
		return nil, false
	}
	items, ok := clone[field].([]interface{})
	if !ok || itemIdx < 0 || itemIdx >= len(items) {
		return nil, false
	}
	items = items[:itemIdx+1]
	if partIdx >= 0 {
		msg, ok := items[itemIdx].(map[string]interface{})
		if !ok {
			return nil, false
		}
		content, ok := msg["content"].([]interface{})
		if !ok || partIdx >= len(content) {
			return nil, false
		}
		msg["content"] = content[:partIdx+1]
	}
	clone[field] = items
	deleteCacheControlRecursive(clone)
	rawPrefix, err := json.Marshal(clone)
	if err != nil {
		return nil, false
	}
	return rawPrefix, true
}

func collectCacheControlMarkedJSON(value interface{}, out *[]cacheControlPrefix) {
	switch v := value.(type) {
	case map[string]interface{}:
		if _, ok := v["cache_control"]; ok {
			if raw, err := json.Marshal(v); err == nil {
				*out = append(*out, cacheControlPrefix{raw: raw})
			}
		}
		for _, child := range v {
			collectCacheControlMarkedJSON(child, out)
		}
	case []interface{}:
		for _, child := range v {
			collectCacheControlMarkedJSON(child, out)
		}
	}
}

func decodeJSONUseNumber(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}
