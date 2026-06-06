package convert

import (
	"encoding/json"
	"strings"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func projectPromptCacheKeyForTarget(req *relayir.Request, upstreamFormat Format) {
	if req == nil || !(upstreamFormat == FormatOpenAIChatCompletions || isResponsesFamily(upstreamFormat)) {
		return
	}
	if req.Metadata == nil {
		req.Metadata = map[string]json.RawMessage{}
	}
	if rawString(req.Metadata["prompt_cache_key"]) != "" {
		return
	}
	if key := promptCacheKeyFromMetadata(req.Metadata); key != "" {
		req.Metadata["prompt_cache_key"] = rawJSON(key)
	}
}

func promptCacheKeyFromMetadata(metadata map[string]json.RawMessage) string {
	for _, key := range []string{"prompt_cache_key", "session_id", "sessionId", "conversation_id", "conversationId"} {
		if value := rawString(metadata[key]); value != "" {
			return value
		}
	}
	if raw := metadata["metadata"]; len(raw) > 0 {
		if key := promptCacheKeyFromJSONValue(raw); key != "" {
			return key
		}
	}
	if raw := metadata["client_metadata"]; len(raw) > 0 {
		if key := promptCacheKeyFromJSONValue(raw); key != "" {
			return key
		}
	}
	return ""
}

func promptCacheKeyFromJSONValue(raw json.RawMessage) string {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, key := range []string{"prompt_cache_key", "session_id", "sessionId", "conversation_id", "conversationId"} {
		if value := rawString(obj[key]); value != "" {
			return value
		}
	}
	if raw := obj["user_id"]; len(raw) > 0 {
		if key := promptCacheKeyFromPossiblyJSONString(raw); key != "" {
			return key
		}
	}
	if raw := obj["user"]; len(raw) > 0 {
		if key := promptCacheKeyFromPossiblyJSONString(raw); key != "" {
			return key
		}
	}
	return ""
}

func promptCacheKeyFromPossiblyJSONString(raw json.RawMessage) string {
	if key := promptCacheKeyFromJSONValue(raw); key != "" {
		return key
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "{") {
		return ""
	}
	return promptCacheKeyFromJSONValue(json.RawMessage(text))
}
