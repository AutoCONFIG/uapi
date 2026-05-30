package convert

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// OpenAIResponsesToInternal converts OpenAI Responses API request to InternalRequest.
func OpenAIResponsesToInternal(body []byte) (*InternalRequest, error) {
	var req schema.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses request: %w", err)
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(body, &rawRoot)

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
	copyRawFields(ir.Extra, rawRoot,
		"truncation",
		"stream_options",
		"metadata",
		"user",
		"previous_response_id",
		"include",
		"text",
		"max_tool_calls",
		"conversation",
		"prompt_cache_key",
		"safety_identifier",
	)

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
		rawItem := append(json.RawMessage(nil), item.Raw...)
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
				ItemID:  item.ID,
				Status:  item.Status,
				Phase:   item.Phase,
				RawItem: rawItem,
				Extra:   item.Extra,
			})

		case "reasoning":
			messages = append(messages, InternalMessage{
				Role:             "assistant",
				ReasoningContent: reasoningPartsFromResponsesExtra(item.Extra),
				ItemID:           item.ID,
				Status:           item.Status,
				RawItem:          rawItem,
				Extra:            item.Extra,
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
				ItemID:  item.ID,
				Status:  item.Status,
				RawItem: rawItem,
				Extra:   item.Extra,
			})

		case "function_call_output":
			// function_call_output becomes tool result
			messages = append(messages, InternalMessage{
				Role: "tool",
				ToolResult: &schema.ToolResult{
					ToolCallID: item.CallID,
					Content:    item.Output,
				},
				ItemID:  item.ID,
				Status:  item.Status,
				RawItem: rawItem,
				Extra:   item.Extra,
			})
		default:
			messages = append(messages, InternalMessage{
				ItemID:  item.ID,
				Status:  item.Status,
				Phase:   item.Phase,
				RawItem: rawItem,
				Extra:   item.Extra,
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
	if _, ok := rawRoot["parallel_tool_calls"]; ok {
		ir.ParallelToolCalls = &req.ParallelToolCalls
	} else if req.ParallelToolCalls {
		ir.ParallelToolCalls = &req.ParallelToolCalls
	}
	if req.ServiceTier != "" {
		ir.ServiceTier = req.ServiceTier
	}
	if _, ok := rawRoot["store"]; ok {
		ir.Store = &req.Store
	} else if req.Store {
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
		if ir.SourceFormat == FormatOpenAIResponses && len(msg.RawItem) > 0 {
			var raw map[string]interface{}
			if err := json.Unmarshal(msg.RawItem, &raw); err == nil {
				input = append(input, raw)
				continue
			}
		}

		if len(msg.ReasoningContent) > 0 {
			reasoning := map[string]interface{}{
				"type":    "reasoning",
				"id":      generateResponsesReasoningID(),
				"summary": responsesReasoningSummary(msg.ReasoningContent),
			}
			if msg.ItemID != "" {
				reasoning["id"] = msg.ItemID
			}
			if msg.Status != "" {
				reasoning["status"] = msg.Status
			}
			if encrypted := reasoningEncryptedContent(msg.ReasoningContent); encrypted != "" {
				reasoning["encrypted_content"] = encrypted
			}
			input = append(input, reasoning)
		}

		switch msg.Role {
		case "user", "assistant":
			if len(msg.Content) == 0 && len(msg.ToolCalls) == 0 {
				continue
			}
			if len(msg.Content) > 0 {
				item := map[string]interface{}{
					"type":    "message",
					"role":    msg.Role,
					"content": responsesMessageContent(msg),
				}
				if msg.ItemID != "" {
					item["id"] = msg.ItemID
				}
				if msg.Status != "" {
					item["status"] = msg.Status
				}
				if msg.Phase != "" {
					item["phase"] = msg.Phase
				}
				for k, v := range msg.Extra {
					item[k] = v
				}
				input = append(input, item)
			}
			for _, tc := range msg.ToolCalls {
				name := tc.Name
				if name == "" {
					name = tc.Function.Name
				}
				input = append(input, map[string]interface{}{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      name,
					"arguments": tc.Function.Arguments,
				})
			}

		case "tool":
			// Tool result becomes function_call_output
			if msg.ToolResult == nil {
				continue
			}
			input = append(input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": msg.ToolResult.ToolCallID,
				"output":  msg.ToolResult.Content,
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

func generateResponsesReasoningID() string {
	return fmt.Sprintf("rs_%x", time.Now().UnixNano())
}

func copyRawFields(dst map[string]json.RawMessage, src map[string]json.RawMessage, keys ...string) {
	if dst == nil || src == nil {
		return
	}
	for _, key := range keys {
		if raw, ok := src[key]; ok {
			dst[key] = append(json.RawMessage(nil), raw...)
		}
	}
}

func responsesMessageContent(msg InternalMessage) interface{} {
	if len(msg.Content) == 1 && msg.Role != "assistant" {
		part := msg.Content[0]
		if (part.Type == "text" || part.Type == "input_text") && len(part.Extra) == 0 {
			return part.Text
		}
	}

	parts := make([]map[string]interface{}, 0, len(msg.Content))
	for _, part := range msg.Content {
		parts = append(parts, responsesContentPartMap(msg.Role, part))
	}
	return parts
}

func responsesContentPartMap(role string, part schema.ContentPart) map[string]interface{} {
	out := make(map[string]interface{}, len(part.Extra)+6)
	for key, value := range part.Extra {
		out[key] = value
	}

	partType := part.Type
	switch partType {
	case "text":
		if role == "assistant" {
			partType = "output_text"
		} else {
			partType = "input_text"
		}
	case "image_url":
		partType = "input_image"
	case "file":
		partType = "input_file"
	}
	if partType != "" {
		out["type"] = partType
	}
	if part.Text != "" {
		out["text"] = part.Text
	}
	if part.ImageURL != nil {
		out["image_url"] = *part.ImageURL
	}
	if part.ImageDetail != "" {
		out["detail"] = part.ImageDetail
	}
	if part.Data != "" {
		out["data"] = part.Data
	}
	if part.MimeType != "" {
		out["mime_type"] = part.MimeType
	}
	if part.Refusal != "" {
		out["refusal"] = part.Refusal
	}
	return out
}

func init() {
	RegisterToInternal(FormatOpenAIResponses, OpenAIResponsesToInternal)
	RegisterFromInternal(FormatOpenAIResponses, InternalToOpenAIResponses)
}
