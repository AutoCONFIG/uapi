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

func appendContentItem(msg *requestTurnDraft, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, requestItemDraft{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendReasoningItem(msg *requestTurnDraft, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, requestItemDraft{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolCallItem(msg *requestTurnDraft, call schema.ToolCall, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, requestItemDraft{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolResultItem(msg *requestTurnDraft, result schema.ToolResult, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, requestItemDraft{Kind: contentItemKindToolResult, ToolResult: result, Raw: append(json.RawMessage(nil), raw...)})
}

func canonicalMessageParts(msg requestTurnDraft) []requestItemDraft {
	return msg.Parts
}

func contentPartsFromItems(items []requestItemDraft) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindContent {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func reasoningPartsFromItems(items []requestItemDraft) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindReasoning {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func toolCallsFromItems(items []requestItemDraft) []schema.ToolCall {
	calls := make([]schema.ToolCall, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindToolCall {
			calls = append(calls, item.ToolCall)
		}
	}
	return calls
}

func toolResultFromItems(items []requestItemDraft) *schema.ToolResult {
	for _, item := range items {
		if item.Kind == contentItemKindToolResult {
			result := item.ToolResult
			return &result
		}
	}
	return nil
}

func appendChoiceContentItem(choice *responseChoiceDraft, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, requestItemDraft{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceReasoningItem(choice *responseChoiceDraft, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, requestItemDraft{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceToolCallItem(choice *responseChoiceDraft, call schema.ToolCall, raw json.RawMessage) {
	choice.Items = append(choice.Items, requestItemDraft{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceRefusalItem(choice *responseChoiceDraft, refusal string) {
	choice.Items = append(choice.Items, requestItemDraft{Kind: "refusal", Content: schema.ContentPart{Type: "refusal", Refusal: refusal}})
}

func canonicalChoiceItems(choice responseChoiceDraft) []requestItemDraft {
	return choice.Items
}
