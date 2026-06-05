package channelcap

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
)

const (
	KindChatCompletion  = "chat_completion"
	KindResponses       = "responses"
	KindMessages        = "messages"
	KindGeminiGenerate  = "gemini_generate"
	KindImageGeneration = "image_generation"
	KindImageEdit       = "image_edit"
	KindImageVariation  = "image_variation"
	KindSpeech          = "speech"
	KindTranscription   = "transcription"
	KindTranslation     = "translation"
	KindEmbedding       = "embedding"
	KindModeration      = "moderation"
	KindRealtime        = "realtime"
	KindVideoGeneration = "video_generation"
)

type Request struct {
	Kind      string
	HasTools  bool
	HasImages bool
}

func AnalyzeJSON(kind string, body []byte) Request {
	req := Request{Kind: kind}
	if len(body) == 0 || !strings.Contains(string(body), "{") {
		return req
	}
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return req
	}
	req.HasTools = hasNonEmpty(root["tools"]) || hasNonAutoToolChoice(root["tool_choice"])
	req.HasImages = jsonContainsImage(root["messages"]) || jsonContainsImage(root["input"]) || jsonContainsImage(root["contents"])
	return req
}

func Supports(ch db.Channel, req Request) bool {
	if ch.APIFormat == "chatgpt_reverse" {
		return supportsChatGPTReverse(req)
	}
	return supportsBaseChannelType(ch.Type, req.Kind)
}

func supportsChatGPTReverse(req Request) bool {
	switch req.Kind {
	case KindChatCompletion:
		if req.HasTools || req.HasImages {
			return false
		}
		return true
	case KindImageGeneration:
		return true
	default:
		return false
	}
}

func supportsBaseChannelType(channelType, kind string) bool {
	switch kind {
	case KindImageGeneration, KindImageEdit, KindImageVariation:
		return channelType == "openai" || channelType == "antigravity"
	case KindSpeech, KindTranscription, KindTranslation:
		return channelType == "openai"
	case KindEmbedding, KindModeration, KindRealtime, KindVideoGeneration:
		return channelType == "openai"
	default:
		return true
	}
}

func hasNonEmpty(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case []interface{}:
		return len(v) > 0
	case map[string]interface{}:
		return len(v) > 0
	case string:
		return strings.TrimSpace(v) != ""
	default:
		return true
	}
}

func hasNonAutoToolChoice(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		v = strings.ToLower(strings.TrimSpace(v))
		return v != "" && v != "none" && v != "auto"
	default:
		return true
	}
}

func jsonContainsImage(value interface{}) bool {
	switch v := value.(type) {
	case []interface{}:
		for _, item := range v {
			if jsonContainsImage(item) {
				return true
			}
		}
	case map[string]interface{}:
		if typ, _ := v["type"].(string); strings.Contains(strings.ToLower(typ), "image") {
			return true
		}
		if _, ok := v["image_url"]; ok {
			return true
		}
		if _, ok := v["inline_data"]; ok {
			return true
		}
		for _, item := range v {
			if jsonContainsImage(item) {
				return true
			}
		}
	}
	return false
}
