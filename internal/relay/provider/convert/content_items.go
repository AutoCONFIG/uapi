package convert

import (
	"encoding/json"
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
