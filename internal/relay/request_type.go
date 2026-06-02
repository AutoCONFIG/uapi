package relay

import (
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

type relayRequestType string

const (
	requestTypeChatCompletion  relayRequestType = "chat_completion"
	requestTypeResponses       relayRequestType = "responses"
	requestTypeMessages        relayRequestType = "messages"
	requestTypeGeminiGenerate  relayRequestType = "gemini_generate"
	requestTypeImageGeneration relayRequestType = "image_generation"
	requestTypeImageEdit       relayRequestType = "image_edit"
	requestTypeImageVariation  relayRequestType = "image_variation"
	requestTypeSpeech          relayRequestType = "speech"
	requestTypeTranscription   relayRequestType = "transcription"
	requestTypeTranslation     relayRequestType = "translation"
	requestTypeEmbedding       relayRequestType = "embedding"
	requestTypeModeration      relayRequestType = "moderation"
	requestTypeRealtime        relayRequestType = "realtime"
	requestTypeVideoGeneration relayRequestType = "video_generation"
	requestTypeUnsupported     relayRequestType = "unsupported"
)

func detectRelayRequestType(path string) relayRequestType {
	switch {
	case path == "/v1/chat/completions" || path == "/v1/chat/completions/":
		return requestTypeChatCompletion
	case strings.HasPrefix(path, "/v1/async/"):
		return requestTypeUnsupported
	case strings.HasPrefix(path, "/v1/responses"):
		if path == "/v1/responses" || path == "/v1/responses/" {
			return requestTypeResponses
		}
		return requestTypeUnsupported
	case strings.HasPrefix(path, "/v1/messages"):
		if path == "/v1/messages" || path == "/v1/messages/" {
			return requestTypeMessages
		}
		return requestTypeUnsupported
	case strings.HasPrefix(path, "/v1beta/"):
		if isUnsupportedGeminiRoute(path) {
			return requestTypeUnsupported
		}
		return requestTypeGeminiGenerate
	case strings.HasPrefix(path, "/v1/files") ||
		strings.HasPrefix(path, "/v1/containers") ||
		strings.HasPrefix(path, "/v1/batches"):
		return requestTypeUnsupported
	case strings.HasPrefix(path, "/v1/images/generations"):
		return requestTypeImageGeneration
	case strings.HasPrefix(path, "/v1/images/edits"):
		return requestTypeImageEdit
	case strings.HasPrefix(path, "/v1/images/variations"):
		return requestTypeImageVariation
	case strings.HasPrefix(path, "/v1/audio/speech"):
		return requestTypeSpeech
	case strings.HasPrefix(path, "/v1/audio/transcriptions"):
		return requestTypeTranscription
	case strings.HasPrefix(path, "/v1/audio/translations"):
		return requestTypeTranslation
	case strings.HasPrefix(path, "/v1/embeddings"):
		return requestTypeEmbedding
	case strings.HasPrefix(path, "/v1/moderations"):
		return requestTypeModeration
	case strings.HasPrefix(path, "/v1/realtime/"):
		return requestTypeRealtime
	case strings.HasPrefix(path, "/v1/videos") || strings.HasPrefix(path, "/v1/video/"):
		return requestTypeVideoGeneration
	default:
		return requestTypeUnsupported
	}
}

func isUnsupportedGeminiRoute(path string) bool {
	switch {
	case strings.HasPrefix(path, "/upload/v1beta/files"):
		return true
	case strings.HasPrefix(path, "/v1beta/files"),
		strings.HasPrefix(path, "/v1beta/cachedContents"),
		strings.HasPrefix(path, "/v1beta/batches"),
		strings.HasPrefix(path, "/v1beta/operations"):
		return true
	}
	for _, action := range []string{
		":countTokens",
		":embedContent",
		":batchEmbedContents",
		":predict",
		":predictLongRunning",
		":batchGenerateContent",
	} {
		if strings.Contains(path, action) {
			return true
		}
	}
	return false
}

func (rt relayRequestType) clientFormat() provider.Format {
	switch rt {
	case requestTypeResponses:
		return provider.FormatOpenAIResponses
	case requestTypeMessages:
		return provider.FormatAnthropic
	case requestTypeGeminiGenerate:
		return provider.FormatGemini
	case requestTypeUnsupported:
		return ""
	default:
		return provider.FormatOpenAIChatCompletions
	}
}

func (rt relayRequestType) permission() string {
	switch rt {
	case requestTypeResponses:
		return "responses"
	case requestTypeMessages:
		return "messages"
	case requestTypeGeminiGenerate:
		return "gemini"
	case requestTypeImageGeneration, requestTypeImageEdit, requestTypeImageVariation:
		return "images"
	case requestTypeSpeech, requestTypeTranscription, requestTypeTranslation:
		return "audio"
	case requestTypeEmbedding:
		return "embeddings"
	case requestTypeModeration:
		return "moderations"
	case requestTypeRealtime:
		return "realtime"
	case requestTypeVideoGeneration:
		return "videos"
	case requestTypeUnsupported:
		return ""
	default:
		return "chat"
	}
}

func (rt relayRequestType) isMedia() bool {
	switch rt {
	case requestTypeImageGeneration, requestTypeImageEdit, requestTypeImageVariation,
		requestTypeSpeech, requestTypeTranscription, requestTypeTranslation,
		requestTypeEmbedding, requestTypeModeration, requestTypeRealtime,
		requestTypeVideoGeneration:
		return true
	default:
		return false
	}
}

func (rt relayRequestType) isImage() bool {
	switch rt {
	case requestTypeImageGeneration, requestTypeImageEdit, requestTypeImageVariation:
		return true
	default:
		return false
	}
}

func supportsRelayRequestType(channelType string, rt relayRequestType) bool {
	switch rt {
	case requestTypeImageGeneration:
		return channelType == "openai" || channelType == "antigravity"
	case requestTypeImageEdit, requestTypeImageVariation:
		return channelType == "openai" || channelType == "antigravity"
	case requestTypeSpeech, requestTypeTranscription, requestTypeTranslation:
		return channelType == "openai"
	case requestTypeEmbedding, requestTypeModeration, requestTypeRealtime, requestTypeVideoGeneration:
		return channelType == "openai"
	default:
		return true
	}
}
