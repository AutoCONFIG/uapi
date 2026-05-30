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

func appendContentItem(msg *InternalMessage, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendReasoningItem(msg *InternalMessage, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolCallItem(msg *InternalMessage, call schema.ToolCall, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolResultItem(msg *InternalMessage, result schema.ToolResult, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindToolResult, ToolResult: result, Raw: append(json.RawMessage(nil), raw...)})
}

func canonicalMessageParts(msg InternalMessage) []InternalContentItem {
	return msg.Parts
}

func contentPartsFromItems(items []InternalContentItem) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindContent {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func reasoningPartsFromItems(items []InternalContentItem) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindReasoning {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func toolCallsFromItems(items []InternalContentItem) []schema.ToolCall {
	calls := make([]schema.ToolCall, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindToolCall {
			calls = append(calls, item.ToolCall)
		}
	}
	return calls
}

func toolResultFromItems(items []InternalContentItem) *schema.ToolResult {
	for _, item := range items {
		if item.Kind == contentItemKindToolResult {
			result := item.ToolResult
			return &result
		}
	}
	return nil
}
