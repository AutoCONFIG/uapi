package convert

import (
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

const (
	contentItemKindContent    = "content"
	contentItemKindReasoning  = "reasoning"
	contentItemKindToolCall   = "tool_call"
	contentItemKindToolResult = "tool_result"
)

func rawJSON(v interface{}) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func appendContentItem(msg *adapterTurn, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, adapterItem{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendReasoningItem(msg *adapterTurn, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, adapterItem{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolCallItem(msg *adapterTurn, call schema.ToolCall, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, adapterItem{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolResultItem(msg *adapterTurn, result schema.ToolResult, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, adapterItem{Kind: contentItemKindToolResult, ToolResult: result, Raw: append(json.RawMessage(nil), raw...)})
}

func canonicalMessageParts(msg adapterTurn) []adapterItem {
	return msg.Parts
}

func contentPartsFromItems(items []adapterItem) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindContent {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func reasoningPartsFromItems(items []adapterItem) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindReasoning {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func toolCallsFromItems(items []adapterItem) []schema.ToolCall {
	calls := make([]schema.ToolCall, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindToolCall {
			calls = append(calls, item.ToolCall)
		}
	}
	return calls
}

func toolResultFromItems(items []adapterItem) *schema.ToolResult {
	for _, item := range items {
		if item.Kind == contentItemKindToolResult {
			result := item.ToolResult
			return &result
		}
	}
	return nil
}

func appendChoiceContentItem(choice *adapterChoice, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, adapterItem{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceReasoningItem(choice *adapterChoice, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, adapterItem{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceToolCallItem(choice *adapterChoice, call schema.ToolCall, raw json.RawMessage) {
	choice.Items = append(choice.Items, adapterItem{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceRefusalItem(choice *adapterChoice, refusal string) {
	choice.Items = append(choice.Items, adapterItem{Kind: "refusal", Content: schema.ContentPart{Type: "refusal", Refusal: refusal}})
}

func canonicalChoiceItems(choice adapterChoice) []adapterItem {
	return choice.Items
}
