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

func appendContentItem(msg *protocolTurnView, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, protocolItemView{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendReasoningItem(msg *protocolTurnView, part schema.ContentPart, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, protocolItemView{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolCallItem(msg *protocolTurnView, call schema.ToolCall, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, protocolItemView{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolResultItem(msg *protocolTurnView, result schema.ToolResult, raw json.RawMessage) {
	msg.Parts = append(msg.Parts, protocolItemView{Kind: contentItemKindToolResult, ToolResult: result, Raw: append(json.RawMessage(nil), raw...)})
}

func canonicalMessageParts(msg protocolTurnView) []protocolItemView {
	return msg.Parts
}

func contentPartsFromItems(items []protocolItemView) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindContent {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func reasoningPartsFromItems(items []protocolItemView) []schema.ContentPart {
	parts := make([]schema.ContentPart, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindReasoning {
			parts = append(parts, item.Content)
		}
	}
	return parts
}

func toolCallsFromItems(items []protocolItemView) []schema.ToolCall {
	calls := make([]schema.ToolCall, 0, len(items))
	for _, item := range items {
		if item.Kind == contentItemKindToolCall {
			calls = append(calls, item.ToolCall)
		}
	}
	return calls
}

func toolResultFromItems(items []protocolItemView) *schema.ToolResult {
	for _, item := range items {
		if item.Kind == contentItemKindToolResult {
			result := item.ToolResult
			return &result
		}
	}
	return nil
}

func appendChoiceContentItem(choice *protocolChoiceView, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, protocolItemView{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceReasoningItem(choice *protocolChoiceView, part schema.ContentPart, raw json.RawMessage) {
	choice.Items = append(choice.Items, protocolItemView{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceToolCallItem(choice *protocolChoiceView, call schema.ToolCall, raw json.RawMessage) {
	choice.Items = append(choice.Items, protocolItemView{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendChoiceRefusalItem(choice *protocolChoiceView, refusal string) {
	choice.Items = append(choice.Items, protocolItemView{Kind: "refusal", Content: schema.ContentPart{Type: "refusal", Refusal: refusal}})
}

func canonicalChoiceItems(choice protocolChoiceView) []protocolItemView {
	return choice.Items
}
