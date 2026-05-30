package convert

import (
	"encoding/json"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func (r *InternalResponse) ToIR(source Format) *relayir.Response {
	if r == nil {
		return nil
	}
	resp := &relayir.Response{
		SourceProtocol: irProtocol(source),
		ID:             r.ID,
		Model:          r.Model,
		Usage:          usageToIR(r.Usage),
		Native:         relayir.NativeEnvelope{Protocol: irProtocol(source), Raw: relayir.CloneRaw(r.Raw)},
	}
	for _, choice := range r.Choices {
		resp.Choices = append(resp.Choices, choiceToIR(choice, source))
	}
	return resp
}

func choiceToIR(choice InternalChoice, source Format) relayir.Choice {
	out := relayir.Choice{
		Index: choice.Index,
		Role:  irRole(choice.Role),
		Finish: &relayir.Finish{
			Reason:       finishReasonToIR(choice.FinishReason),
			NativeReason: choice.FinishReason,
		},
	}
	idx := 0
	for _, part := range choice.ReasoningContent {
		out.Items = append(out.Items, irContentPartItem(contentItemKindReasoning, part, nil, source, idx))
		idx++
	}
	for _, part := range choice.Content {
		out.Items = append(out.Items, irContentPartItem(contentItemKindContent, part, nil, source, idx))
		idx++
	}
	for _, call := range choice.ToolCalls {
		out.Items = append(out.Items, irToolUseItem(call, nil, source, idx))
		idx++
	}
	if choice.Refusal != "" {
		out.Items = append(out.Items, relayir.Item{
			OriginalIndex: idx,
			Kind:          relayir.ItemRefusal,
			Refusal:       &relayir.Refusal{Text: choice.Refusal},
			Native:        relayir.NativeEnvelope{Protocol: irProtocol(source), Kind: "refusal", Index: idx},
		})
	}
	return out
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
