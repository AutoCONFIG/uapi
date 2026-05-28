package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// OpenAIResponsesToInternal converts OpenAI Responses API request to InternalRequest.
func OpenAIResponsesToInternal(body []byte) (*InternalRequest, error) {
	var req schema.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses request: %w", err)
	}

	ir := &InternalRequest{
		Model:        req.Model,
		Stream:       req.Stream,
		SourceFormat: FormatOpenAIResponses,
		Extra:        make(map[string]json.RawMessage),
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
	}

	// Handle Instructions - always set (the bug fix)
	if req.Instructions != "" {
		ir.Instructions = &req.Instructions
	}

	// Parse Input - can be string or array
	var messages []InternalMessage
	var inputItems []schema.ResponsesInputItem

	if req.Input.Text != nil {
		// Single string input becomes a user message
		messages = append(messages, InternalMessage{
			Role:    "user",
			Content: []schema.ContentPart{{Type: "text", Text: *req.Input.Text}},
		})
	} else if len(req.Input.Items) > 0 {
		inputItems = req.Input.Items
	} else {
		// Empty input - no messages
	}

	// Convert input items to InternalMessages
	for _, item := range inputItems {
		switch item.Type {
		case "message":
			var content []schema.ContentPart
			if item.Content.Text != nil {
				content = []schema.ContentPart{{Type: "text", Text: *item.Content.Text}}
			} else if len(item.Content.Parts) > 0 {
				content = item.Content.Parts
			}
			messages = append(messages, InternalMessage{
				Role:    item.Role,
				Content: content,
			})

		case "function_call":
			// function_call item becomes assistant message with tool calls
			messages = append(messages, InternalMessage{
				Role: "assistant",
				ToolCalls: []schema.ToolCall{{
					ID:   item.CallID,
					Type: "function",
					Name: item.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				}},
			})

		case "function_call_output":
			// function_call_output becomes tool result
			messages = append(messages, InternalMessage{
				Role: "tool",
				ToolResult: &schema.ToolResult{
					ToolCallID: item.CallID,
					Content:    item.Output,
				},
			})
		}
	}

	ir.Messages = messages

	// Generation parameters
	if req.MaxOutputTokens != nil {
		ir.MaxTokens = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		ir.Temperature = req.Temperature
	}
	if req.TopP != nil {
		ir.TopP = req.TopP
	}
	if req.ParallelToolCalls {
		ir.ParallelToolCalls = &req.ParallelToolCalls
	}
	if req.ServiceTier != "" {
		ir.ServiceTier = req.ServiceTier
	}
	if req.Store {
		ir.Store = &req.Store
	}
	if req.Reasoning != nil {
		ir.Reasoning = req.Reasoning
	}
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			ir.Tools = tools
		}
	}
	if req.ToolChoice != nil {
		ir.ToolChoice = req.ToolChoice
	}

	return ir, nil
}

// InternalToOpenAIResponses converts InternalRequest to OpenAI Responses API request.
// THIS IS WHERE THE BUG FIX IS - instructions always emitted
func InternalToOpenAIResponses(ir *InternalRequest) ([]byte, error) {
	// Use a map to build the JSON to ensure field ordering
	resp := make(map[string]interface{})

	resp["model"] = ir.Model
	resp["stream"] = ir.Stream

	// CRITICAL BUG FIX: Always emit instructions field, even if empty
	// The old code was: if ir.Instructions != nil && *ir.Instructions != "" { ... }
	// This caused "Instructions are required" error when no system message was present
	if ir.Instructions != nil {
		resp["instructions"] = *ir.Instructions
	} else {
		resp["instructions"] = "" // Always emit, even if empty
	}

	// Convert messages to input array
	input := make([]map[string]interface{}, 0)
	for _, msg := range ir.Messages {
		switch msg.Role {
		case "user", "assistant":
			// Build content
			var content interface{}
			if len(msg.Content) == 1 && msg.Content[0].Type == "text" {
				content = msg.Content[0].Text
			} else if len(msg.Content) > 0 {
				parts := make([]map[string]string, 0, len(msg.Content))
				for _, p := range msg.Content {
					part := map[string]string{"type": p.Type}
					if p.Text != "" {
						part["text"] = p.Text
					}
					if p.ImageURL != nil {
						part["image_url"] = *p.ImageURL
					}
					parts = append(parts, part)
				}
				content = parts
			}

			input = append(input, map[string]interface{}{
				"type":    "message",
				"role":    msg.Role,
				"content": content,
			})

		case "tool":
			// Tool result becomes function_call_output
			input = append(input, map[string]interface{}{
				"type":   "function_call_output",
				"call_id": msg.ToolResult.ToolCallID,
				"output": msg.ToolResult.Content,
			})
		}
	}
	resp["input"] = input

	// Generation parameters
	if ir.MaxTokens != nil {
		resp["max_output_tokens"] = *ir.MaxTokens
	}
	if ir.Temperature != nil {
		resp["temperature"] = *ir.Temperature
	}
	if ir.TopP != nil {
		resp["top_p"] = *ir.TopP
	}
	if ir.ParallelToolCalls != nil {
		resp["parallel_tool_calls"] = *ir.ParallelToolCalls
	}
	if ir.ServiceTier != "" {
		resp["service_tier"] = ir.ServiceTier
	}
	if ir.Store != nil {
		resp["store"] = *ir.Store
	}
	if ir.Reasoning != nil {
		resp["reasoning"] = ir.Reasoning
	}
	if ir.Tools != nil {
		tools, _ := json.Marshal(ir.Tools)
		resp["tools"] = json.RawMessage(tools)
	}
	if ir.ToolChoice != nil {
		tc, _ := json.Marshal(ir.ToolChoice)
		resp["tool_choice"] = json.RawMessage(tc)
	}

	// Add Extra fields
	for k, v := range ir.Extra {
		resp[k] = v
	}

	return json.Marshal(resp)
}

func init() {
	RegisterToInternal(FormatOpenAIResponses, OpenAIResponsesToInternal)
	RegisterFromInternal(FormatOpenAIResponses, InternalToOpenAIResponses)
}