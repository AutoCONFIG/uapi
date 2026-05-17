package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

// anthropicResponseToInternal parses an Anthropic Messages API response into InternalResponse.
func anthropicResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse anthropic response: %w", err)
	}

	ir := &provider.InternalResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []provider.InternalChoice{
			{Index: 0},
		},
		Usage: provider.InternalUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
		},
	}

	// Parse content — could be a string or array of blocks
	var contentBlocks []map[string]interface{}
	if err := json.Unmarshal(resp.Content, &contentBlocks); err == nil {
		for _, block := range contentBlocks {
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				ir.Choices[0].Message.Content = append(ir.Choices[0].Message.Content, provider.InternalContentPart{
					Type: "text",
					Text: text,
				})
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				args := "{}"
				if inputVal, ok := block["input"]; ok {
					if b, err := json.Marshal(inputVal); err == nil {
						args = string(b)
					}
				}
				ir.Choices[0].Message.ToolCalls = append(ir.Choices[0].Message.ToolCalls, provider.InternalToolCall{
					ID:        id,
					Name:      name,
					Arguments: args,
				})
			}
		}
	}

	// Build the single choice with mapped finish reason
	finishReason := mapFinishReasonToInternal(resp.StopReason)
	ir.Choices[0].FinishReason = finishReason

	return ir, nil
}

// mapFinishReasonToInternal maps Anthropic stop_reason to InternalResponse finish reason.
func mapFinishReasonToInternal(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

// internalToAnthropicResponse serializes an InternalResponse into Anthropic Messages API format.
func internalToAnthropicResponse(resp *provider.InternalResponse) ([]byte, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in internal response")
	}

	choice := resp.Choices[0]

	// Build content blocks from the message
	type contentBlock struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}

	var blocks []contentBlock

	// Add text content parts
	for _, part := range choice.Message.Content {
		if part.Type == "text" {
			blocks = append(blocks, contentBlock{
				Type: "text",
				Text: part.Text,
			})
		}
	}

	// Add tool_use blocks
	for _, tc := range choice.Message.ToolCalls {
		// Arguments is a JSON string; use it directly as RawMessage for "input"
		var input json.RawMessage = []byte(tc.Arguments)
		blocks = append(blocks, contentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: input,
		})
	}

	// Map finish reason back to Anthropic stop_reason
	stopReason := mapFinishReasonFromInternal(choice.FinishReason)

	// Build the response
	type anthropicUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}

	anthResp := struct {
		ID           string         `json:"id"`
		Type         string         `json:"type"`
		Role         string         `json:"role"`
		Content      []contentBlock `json:"content"`
		StopReason   string         `json:"stop_reason"`
		StopSequence interface{}    `json:"stop_sequence"`
		Usage        anthropicUsage `json:"usage"`
	}{
		ID:           resp.ID,
		Type:         "message",
		Role:         "assistant",
		Content:      blocks,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage: anthropicUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	b, err := json.Marshal(anthResp)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic response: %w", err)
	}
	return b, nil
}

// mapFinishReasonFromInternal maps InternalResponse finish reason back to Anthropic stop_reason.
func mapFinishReasonFromInternal(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
