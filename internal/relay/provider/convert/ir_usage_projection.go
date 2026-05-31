package convert

import (
	"encoding/json"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func internalFinishReasonFromIR(reason relayir.FinishReason) string {
	switch reason {
	case relayir.FinishStop:
		return "end_turn"
	case relayir.FinishMaxTokens:
		return "max_tokens"
	case relayir.FinishToolCall:
		return "tool_use"
	case relayir.FinishSafety, relayir.FinishContentFilter:
		return "content_filter"
	default:
		return string(reason)
	}
}

func responseUsageFromIR(usage *relayir.Usage) schema.Usage {
	if usage == nil {
		return schema.Usage{}
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	return schema.Usage{
		PromptTokens:             usage.InputTokens,
		CompletionTokens:         usage.OutputTokens,
		TotalTokens:              total,
		CacheReadInputTokens:     usage.CacheReadTokens,
		CacheCreationInputTokens: usage.CacheCreationTokens,
		PromptTokensDetails:      detailsFromRaw(usage.InputTokenDetails),
		CompletionTokensDetails:  detailsFromRaw(usage.OutputTokenDetails),
	}
}

func usageToIR(usage schema.Usage) *relayir.Usage {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 &&
		usage.CacheReadInputTokens == 0 && usage.CacheCreationInputTokens == 0 &&
		len(usage.PromptTokensDetails) == 0 && len(usage.CompletionTokensDetails) == 0 {
		return nil
	}
	return &relayir.Usage{
		InputTokens:         usage.PromptTokens,
		OutputTokens:        usage.CompletionTokens,
		TotalTokens:         usage.TotalTokens,
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		InputTokenDetails:   rawDetails(usage.PromptTokensDetails),
		OutputTokenDetails:  rawDetails(usage.CompletionTokensDetails),
	}
}

func detailsFromRaw(in map[string]json.RawMessage) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, raw := range in {
		var value interface{}
		if err := json.Unmarshal(raw, &value); err == nil {
			out[key] = value
		}
	}
	return out
}

func rawDetails(in map[string]interface{}) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		raw, err := json.Marshal(value)
		if err == nil {
			out[key] = raw
		}
	}
	return out
}

func finishReasonToIR(reason string) relayir.FinishReason {
	switch reason {
	case "", "stop", "end_turn":
		return relayir.FinishStop
	case "length", "max_tokens", "MAX_TOKENS":
		return relayir.FinishMaxTokens
	case "tool_calls", "tool_use", "function_call":
		return relayir.FinishToolCall
	case "content_filter", "SAFETY":
		return relayir.FinishSafety
	default:
		return relayir.FinishUnknown
	}
}
