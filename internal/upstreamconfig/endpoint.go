package upstreamconfig

import (
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
)

func DefaultEndpoint(providerType, apiFormat string) string {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "openai":
		if apiFormat == "chatgpt_reverse" {
			return "https://chatgpt.com"
		}
		if apiFormat == "codex" {
			return "https://chatgpt.com/backend-api/codex"
		}
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		if apiFormat == "gemini_code" {
			return "https://generativelanguage.googleapis.com"
		}
		return "https://generativelanguage.googleapis.com/v1beta"
	case "antigravity":
		return "https://cloudcode-pa.googleapis.com"
	default:
		return ""
	}
}

func AccountEndpoint(channel *db.Channel, account *db.Account) string {
	if account != nil {
		if endpoint := strings.TrimSpace(account.Endpoint); endpoint != "" {
			return endpoint
		}
	}
	if channel != nil {
		if endpoint := strings.TrimSpace(channel.Endpoint); endpoint != "" {
			return endpoint
		}
		return DefaultEndpoint(channel.Type, channel.APIFormat)
	}
	return ""
}
