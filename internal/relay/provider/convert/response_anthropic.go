package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// parseAnthropicResponse converts Anthropic response to adapterResponse.
func parseAnthropicResponse(body []byte) (*adapterResponse, error) {
	var resp schema.AnthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic response: %w", err)
	}

	ir := &adapterResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: make([]adapterChoice, 0, len(resp.Content)),
		Usage: schema.Usage{
			PromptTokens:             resp.Usage.InputTokens,
			CompletionTokens:         resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		},
		Raw: body, // Preserve raw for native replay and field recovery
	}

	// Convert content blocks to a single choice
	choice := adapterChoice{
		Index: 0,
		Role:  resp.Role,
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			appendChoiceContentItem(&choice, schema.ContentPart{
				Type: "text",
				Text: block.Text,
			}, rawJSON(block))

		case "tool_use":
			appendChoiceToolCallItem(&choice, schema.ToolCall{
				ID:   block.ID,
				Type: "function",
				Name: block.Name,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			}, rawJSON(block))

		case "thinking":
			extra := map[string]json.RawMessage{}
			if block.Signature != "" {
				extra = setRawString(extra, reasoningExtraSignature, block.Signature)
			}
			appendChoiceReasoningItem(&choice, schema.ContentPart{
				Type:  "thinking",
				Text:  block.Thinking,
				Extra: extra,
			}, rawJSON(block))
		case "redacted_thinking":
			if raw, ok := block.Extra[reasoningExtraData]; ok && rawString(raw) != "" {
				appendChoiceReasoningItem(&choice, reasoningPartWithExtra("", map[string]json.RawMessage{
					reasoningExtraData:             raw,
					reasoningExtraEncryptedContent: raw,
					reasoningExtraType:             json.RawMessage(`"reasoning.encrypted"`),
				}), rawJSON(block))
			}
		}
	}

	// Map finish reason
	choice.FinishReason = mapAnthropicFinishReason(resp.StopReason)

	// Calculate total tokens
	ir.Usage.TotalTokens = ir.Usage.PromptTokens + ir.Usage.CompletionTokens

	ir.Choices = append(ir.Choices, choice)

	return ir, nil
}

// mapAnthropicFinishReason converts Anthropic stop_reason to internal format.
func mapAnthropicFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	case "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return fr
	}
}

// mapAnthropicResponseFinishReason converts internal finish_reason to Anthropic format.
func mapAnthropicResponseFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	case "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return fr
	}
}

// emitAnthropicResponse converts adapterResponse to Anthropic response.
func emitAnthropicResponse(ir *adapterResponse) ([]byte, error) {
	resp := make(map[string]interface{})

	resp["id"] = ir.ID
	resp["type"] = "message"
	resp["role"] = "assistant"
	resp["model"] = ir.Model

	// Convert content to Anthropic content blocks
	var content []map[string]interface{}
	for _, choice := range ir.Choices {
		for _, item := range canonicalChoiceItems(choice) {
			switch item.Kind {
			case contentItemKindReasoning:
				rc := item.Content
				sig := reasoningSignature([]schema.ContentPart{rc})
				encrypted := reasoningPartEncryptedData(rc)
				if rc.Text == "" && encrypted != "" && sig == "" {
					content = append(content, map[string]interface{}{
						"type": "redacted_thinking",
						"data": encrypted,
					})
					continue
				}
				if rc.Text != "" || sig != "" {
					block := map[string]interface{}{"type": "thinking", "thinking": rc.Text}
					if sig != "" {
						block["signature"] = sig
					}
					content = append(content, block)
				}
			case contentItemKindContent:
				c := item.Content
				block := map[string]interface{}{"type": c.Type}
				if c.Text != "" {
					block["text"] = c.Text
				}
				if c.Refusal != "" {
					block["type"] = "refusal"
					block["refusal"] = c.Refusal
				}
				content = append(content, block)
			case contentItemKindToolCall:
				tc := item.ToolCall
				name := tc.Name
				if name == "" {
					name = tc.Function.Name
				}
				content = append(content, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  name,
					"input": jsonArgumentValue(tc.Function.Arguments),
				})
			case "refusal":
				content = append(content, map[string]interface{}{
					"type":    "refusal",
					"refusal": item.Content.Refusal,
				})
			}
		}

		// Set finish reason
		resp["stop_reason"] = mapAnthropicResponseFinishReason(choice.FinishReason)

		// Only process first choice for now
		break
	}
	resp["content"] = content

	// Convert usage
	resp["usage"] = map[string]interface{}{
		"input_tokens":  ir.Usage.PromptTokens,
		"output_tokens": ir.Usage.CompletionTokens,
	}

	// Add cache tokens if present
	if ir.Usage.CacheCreationInputTokens > 0 {
		resp["usage"].(map[string]interface{})["cache_creation_input_tokens"] = ir.Usage.CacheCreationInputTokens
	}
	if ir.Usage.CacheReadInputTokens > 0 {
		resp["usage"].(map[string]interface{})["cache_read_input_tokens"] = ir.Usage.CacheReadInputTokens
	}

	// If Raw is present, preserve extra fields
	if len(ir.Raw) > 0 {
		var rawMap map[string]json.RawMessage
		if json.Unmarshal(ir.Raw, &rawMap) == nil {
			for k, v := range rawMap {
				switch k {
				case "id", "type", "role", "model", "content", "stop_reason", "usage":
					// Skip standard fields
				default:
					resp[k] = v
				}
			}
		}
	}

	return json.Marshal(resp)
}

func init() {
	registerAdapterResponseParser(FormatAnthropic, parseAnthropicResponse)
	registerAdapterResponseEmitter(FormatAnthropic, emitAnthropicResponse)
}
