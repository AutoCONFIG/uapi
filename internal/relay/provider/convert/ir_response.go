package convert

import (
	"encoding/json"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func (r *responseDraft) ToIR(source Format) *relayir.Response {
	if r == nil {
		return nil
	}
	resp := &relayir.Response{
		SourceProtocol: irProtocol(source),
		ID:             r.ID,
		Model:          r.Model,
		Usage:          usageToIR(r.Usage),
		Native:         relayir.NativeEnvelope{Protocol: irProtocol(source), RawBody: relayir.CloneRaw(r.Raw)},
		Losses:         append([]relayir.Loss(nil), r.Losses...),
	}
	for _, choice := range r.Choices {
		resp.Choices = append(resp.Choices, choiceToIR(choice, source))
	}
	return resp
}

func responseDraftFromIR(resp *relayir.Response) *responseDraft {
	if resp == nil {
		return nil
	}
	out := &responseDraft{
		ID:     resp.ID,
		Model:  resp.Model,
		Usage:  responseUsageFromIR(resp.Usage),
		Raw:    relayir.CloneRaw(resp.Native.RawBody),
		Losses: append([]relayir.Loss(nil), resp.Losses...),
	}
	for _, choice := range resp.Choices {
		out.Choices = append(out.Choices, internalChoiceFromIR(choice))
	}
	return out
}

func internalChoiceFromIR(choice relayir.Choice) responseChoiceDraft {
	out := responseChoiceDraft{
		Index: choice.Index,
		Role:  string(choice.Role),
	}
	if choice.Finish != nil {
		out.FinishReason = choice.Finish.NativeReason
		if out.FinishReason == "" {
			out.FinishReason = internalFinishReasonFromIR(choice.Finish.Reason)
		}
	}
	for _, item := range choice.Items {
		switch item.Kind {
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			appendChoiceReasoningItem(&out, schemaReasoningFromIR(item), relayir.CloneRaw(item.Native.Raw))
		case relayir.ItemToolUse, relayir.ItemFunctionCall:
			appendChoiceToolCallItem(&out, schemaToolCallFromIR(item), relayir.CloneRaw(item.Native.Raw))
		case relayir.ItemRefusal:
			if item.Refusal != nil {
				appendChoiceRefusalItem(&out, item.Refusal.Text)
			}
		default:
			if part, ok := schemaContentFromIR(item); ok {
				appendChoiceContentItem(&out, part, relayir.CloneRaw(item.Native.Raw))
			} else {
				out.Items = append(out.Items, requestItemDraft{Kind: string(item.Kind), Raw: relayir.CloneRaw(item.Native.Raw)})
			}
		}
	}
	return out
}

func choiceToIR(choice responseChoiceDraft, source Format) relayir.Choice {
	out := relayir.Choice{
		Index: choice.Index,
		Role:  irRole(choice.Role),
		Finish: &relayir.Finish{
			Reason:       finishReasonToIR(choice.FinishReason),
			NativeReason: choice.FinishReason,
		},
	}
	for idx, item := range canonicalChoiceItems(choice) {
		switch item.Kind {
		case contentItemKindReasoning:
			out.Items = append(out.Items, irContentPartItem(contentItemKindReasoning, item.Content, item.Raw, source, idx))
		case contentItemKindContent:
			out.Items = append(out.Items, irContentPartItem(contentItemKindContent, item.Content, item.Raw, source, idx))
		case contentItemKindToolCall:
			out.Items = append(out.Items, irToolUseItem(item.ToolCall, item.Raw, source, idx))
		case "refusal":
			out.Items = append(out.Items, relayir.Item{
				OriginalIndex: idx,
				Kind:          relayir.ItemRefusal,
				Refusal:       &relayir.Refusal{Text: item.Content.Refusal},
				Native:        relayir.NativeEnvelope{Protocol: irProtocol(source), Kind: "refusal", Index: idx},
			})
		default:
			out.Items = append(out.Items, relayir.Item{
				OriginalIndex: idx,
				Kind:          relayir.ItemOpaque,
				Opaque:        &relayir.Opaque{Type: item.Kind, Raw: relayir.CloneRaw(item.Raw)},
				Native:        relayir.NativeEnvelope{Protocol: irProtocol(source), Kind: item.Kind, Raw: relayir.CloneRaw(item.Raw), Index: idx},
			})
		}
	}
	return out
}

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
