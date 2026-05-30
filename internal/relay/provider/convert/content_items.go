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
	msg.Content = append(msg.Content, part)
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindContent, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendReasoningItem(msg *InternalMessage, part schema.ContentPart, raw json.RawMessage) {
	msg.ReasoningContent = append(msg.ReasoningContent, part)
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindReasoning, Content: part, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolCallItem(msg *InternalMessage, call schema.ToolCall, raw json.RawMessage) {
	msg.ToolCalls = append(msg.ToolCalls, call)
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindToolCall, ToolCall: call, Raw: append(json.RawMessage(nil), raw...)})
}

func appendToolResultItem(msg *InternalMessage, result schema.ToolResult, raw json.RawMessage) {
	msg.ToolResult = &result
	msg.Parts = append(msg.Parts, InternalContentItem{Kind: contentItemKindToolResult, ToolResult: result, Raw: append(json.RawMessage(nil), raw...)})
}
