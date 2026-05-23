package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
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
			InputTokens             int `json:"input_tokens"`
			OutputTokens            int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse anthropic response: %w", err)
	}

	ir := &provider.InternalResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []provider.InternalChoice{
			{Index: 0, Message: provider.InternalMessage{Role: resp.Role}},
		},
		Usage: provider.InternalUsage{
			PromptTokens:               resp.Usage.InputTokens,
			CompletionTokens:           resp.Usage.OutputTokens,
			CacheCreationInputTokens:   resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:       resp.Usage.CacheReadInputTokens,
		},
	}
	if ir.Choices[0].Message.Role == "" {
		ir.Choices[0].Message.Role = "assistant"
	}

	// Parse content — could be a string or array of blocks
	var contentBlocks []map[string]interface{}
	if len(resp.Content) > 0 {
		if err := provider.DecodeJSONUseNumber(resp.Content, &contentBlocks); err != nil {
			return nil, fmt.Errorf("anthropic response content must be an array of blocks: %w", err)
		}
	}
	for _, block := range contentBlocks {
		if err := validateAnthropicContentBlockKeys(block); err != nil {
			return nil, err
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			text, ok := block["text"].(string)
			if !ok {
				return nil, fmt.Errorf("anthropic response text block requires text")
			}
			ir.Choices[0].Message.Content = append(ir.Choices[0].Message.Content, provider.InternalContentPart{
				Type: "text",
				Text: text,
			})
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if id == "" || name == "" {
				return nil, fmt.Errorf("anthropic response tool_use requires id and name")
			}
			args := "{}"
			if inputVal, ok := block["input"]; ok {
				b, err := json.Marshal(inputVal)
				if err != nil {
					return nil, fmt.Errorf("anthropic response tool_use input must be JSON-serializable: %w", err)
				}
				args = string(b)
			}
			ir.Choices[0].Message.ToolCalls = append(ir.Choices[0].Message.ToolCalls, provider.InternalToolCall{
				ID:        id,
				Name:      name,
				Arguments: args,
			})
		case "thinking", "thinking_delta":
			// Thinking blocks cannot be converted to non-Anthropic formats, skip them
			continue
		default:
			return nil, fmt.Errorf("anthropic response content block type %q cannot be converted to non-anthropic downstream formats", blockType)
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
		args := tc.Arguments
		if args == "" {
			args = "{}"
		} else if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("internal tool call arguments for %q are not valid JSON", tc.Name)
		}
		var input json.RawMessage = []byte(args)
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
		InputTokens             int `json:"input_tokens"`
		OutputTokens            int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
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
			InputTokens:              resp.Usage.PromptTokens,
			OutputTokens:             resp.Usage.CompletionTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
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
