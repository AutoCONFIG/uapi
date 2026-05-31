package convert

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// parseOpenAIResponsesRequest converts OpenAI Responses API request to an protocol request view.
func parseOpenAIResponsesRequest(body []byte) (*requestDraft, error) {
	var req schema.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses request: %w", err)
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(body, &rawRoot)

	ir := &requestDraft{
		Model:          req.Model,
		Stream:         req.Stream,
		RawRequestBody: append(json.RawMessage(nil), body...),
		SourceFormat:   FormatOpenAIResponses,
		Extra:          make(map[string]json.RawMessage),
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
	var messages []requestTurnDraft
	var inputItems []schema.ResponsesInputItem

	if req.Input.Text != nil {
		// Single string input becomes a user message
		msg := requestTurnDraft{Role: "user"}
		appendContentItem(&msg, schema.ContentPart{Type: "text", Text: *req.Input.Text}, nil)
		messages = append(messages, msg)
	} else if len(req.Input.Items) > 0 {
		inputItems = req.Input.Items
	} else {
		// Empty input - no messages
	}

	// Convert input items to adapter turns
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
			msg := requestTurnDraft{
				Role:    item.Role,
				ItemID:  item.ID,
				Status:  item.Status,
				Phase:   item.Phase,
				RawItem: rawItem,
				Extra:   item.Extra,
			}
			for _, part := range content {
				appendContentItem(&msg, part, rawJSON(part))
			}
			messages = append(messages, msg)

		case "reasoning":
			msg := requestTurnDraft{
				Role:    "assistant",
				ItemID:  item.ID,
				Status:  item.Status,
				RawItem: rawItem,
				Extra:   item.Extra,
			}
			for _, part := range reasoningPartsFromResponsesExtra(item.Extra) {
				appendReasoningItem(&msg, part, rawItem)
			}
			messages = append(messages, msg)

		case "function_call":
			// function_call item becomes assistant message with tool calls
			name := qualifyResponsesNamespaceToolName(rawString(item.Extra["namespace"]), item.Name)
			msg := requestTurnDraft{
				Role:    "assistant",
				ItemID:  item.ID,
				Status:  item.Status,
				RawItem: rawItem,
				Extra:   item.Extra,
			}
			appendToolCallItem(&msg, schema.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Name: name,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      name,
					Arguments: item.Arguments,
				},
			}, rawItem)
			messages = append(messages, msg)

		case "function_call_output":
			// function_call_output becomes tool result
			msg := requestTurnDraft{
				Role:    "tool",
				ItemID:  item.ID,
				Status:  item.Status,
				RawItem: rawItem,
				Extra:   item.Extra,
			}
			appendToolResultItem(&msg, schema.ToolResult{
				ToolCallID: item.CallID,
				Content:    item.Output,
				ContentRaw: item.OutputRaw,
			}, rawItem)
			messages = append(messages, msg)
		default:
			ir.Losses = append(ir.Losses, irloss(FormatOpenAIResponses, "", "$.input[]", item.Type, rawItem, "Responses input item is preserved as native raw/opaque IR"))
			messages = append(messages, requestTurnDraft{
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

// emitOpenAIResponsesRequest converts an protocol request view to OpenAI Responses API request.
// THIS IS WHERE THE BUG FIX IS - instructions always emitted
func emitOpenAIResponsesRequest(ir *requestDraft) ([]byte, error) {
	// Use a map to build the JSON to ensure field ordering
	resp := make(map[string]interface{})

	resp["model"] = ir.Model
	resp["stream"] = ir.Stream

	// CRITICAL BUG FIX: Always emit instructions field, even if empty
	// The previous bug was: if ir.Instructions != nil && *ir.Instructions != "" { ... }
	// This caused "Instructions are required" error when no system message was present
	if ir.Instructions != nil {
		resp["instructions"] = *ir.Instructions
	} else {
		resp["instructions"] = "" // Always emit, even if empty
	}

	// Convert messages to input array
	input := make([]map[string]interface{}, 0)
	for _, msg := range ir.Messages {
		if isResponsesFamily(ir.SourceFormat) && len(msg.RawItem) > 0 {
			var raw map[string]interface{}
			if err := decodeJSONUseNumber(msg.RawItem, &raw); err == nil {
				input = append(input, raw)
				continue
			}
		}

		input = append(input, responsesInputItemsFromOrderedParts(msg)...)
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
		tools, _ := json.Marshal(openAIResponsesTools(ir.Tools))
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

func openAIResponsesTools(tools []schema.Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		normalized := openAIResponsesTool(tool)
		if normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func openAIResponsesTool(tool schema.Tool) map[string]interface{} {
	name, description, parameters := normalizedFunctionTool(tool)
	if name != "" {
		out := map[string]interface{}{
			"type": "function",
			"name": name,
		}
		if description != "" {
			out["description"] = description
		}
		if len(parameters) > 0 && string(parameters) != "null" {
			out["parameters"] = json.RawMessage(parameters)
		} else {
			out["parameters"] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		for key, value := range tool.Extra {
			out[key] = value
		}
		if tool.Function != nil {
			for key, value := range tool.Function.Extra {
				out[key] = value
			}
		}
		return out
	}

	toolType := tool.Type
	if toolType == "" {
		return nil
	}
	out := map[string]interface{}{"type": toolType}
	if tool.Name != "" {
		out["name"] = tool.Name
	}
	if tool.Description != "" {
		out["description"] = tool.Description
	}
	if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
		out["parameters"] = json.RawMessage(tool.Parameters)
	}
	for key, value := range tool.Extra {
		out[key] = value
	}
	return out
}

func isResponsesFamily(format Format) bool {
	return format == FormatOpenAIResponses || format == FormatCodexResponses
}

func responsesInputItemsFromOrderedParts(msg requestTurnDraft) []map[string]interface{} {
	var items []map[string]interface{}
	var pendingContent []schema.ContentPart
	flushContent := func() {
		if len(pendingContent) == 0 {
			return
		}
		item := map[string]interface{}{
			"type":    "message",
			"role":    responsesRole(msg.Role),
			"content": responsesMessageContent(responsesRole(msg.Role), pendingContent),
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
		items = append(items, item)
		pendingContent = nil
	}

	for _, part := range msg.Parts {
		switch part.Kind {
		case contentItemKindContent:
			if msg.Role != "tool" {
				pendingContent = append(pendingContent, part.Content)
			}
		case contentItemKindReasoning:
			flushContent()
			reasoning := map[string]interface{}{
				"type":    "reasoning",
				"id":      generateResponsesReasoningID(),
				"summary": responsesReasoningSummary([]schema.ContentPart{part.Content}),
			}
			if msg.ItemID != "" {
				reasoning["id"] = msg.ItemID
			}
			if msg.Status != "" {
				reasoning["status"] = msg.Status
			}
			if encrypted := reasoningEncryptedContent([]schema.ContentPart{part.Content}); encrypted != "" {
				reasoning["encrypted_content"] = encrypted
			}
			items = append(items, reasoning)
		case contentItemKindToolCall:
			flushContent()
			name := part.ToolCall.Name
			if name == "" {
				name = part.ToolCall.Function.Name
			}
			items = append(items, map[string]interface{}{
				"type":      "function_call",
				"call_id":   part.ToolCall.ID,
				"name":      name,
				"arguments": part.ToolCall.Function.Arguments,
			})
		case contentItemKindToolResult:
			flushContent()
			items = append(items, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": part.ToolResult.ToolCallID,
				"output":  responsesToolResultOutput(part.ToolResult),
			})
		}
	}
	flushContent()
	return items
}

func responsesRole(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "function":
		return "tool"
	case "unknown", "opaque", "":
		return "user"
	default:
		return role
	}
}

func responsesToolResultOutput(result schema.ToolResult) interface{} {
	if len(result.ContentRaw) > 0 {
		var value interface{}
		if err := json.Unmarshal(result.ContentRaw, &value); err == nil {
			return value
		}
	}
	return result.Content
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

func responsesMessageContent(role string, content []schema.ContentPart) interface{} {
	if len(content) == 1 && role != "assistant" {
		part := content[0]
		if (part.Type == "text" || part.Type == "input_text") && len(part.Extra) == 0 {
			return part.Text
		}
	}

	parts := make([]map[string]interface{}, 0, len(content))
	for _, part := range content {
		parts = append(parts, responsesContentPartMap(role, part))
	}
	return parts
}

func responsesContentPartMap(role string, part schema.ContentPart) map[string]interface{} {
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
	out := make(map[string]interface{}, len(part.Extra)+6)
	if partType != "input_file" {
		for key, value := range part.Extra {
			if key == "cache_control" {
				continue
			}
			out[key] = value
		}
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
	if part.FileData != "" {
		out["file_data"] = part.FileData
	}
	if part.FileURL != "" {
		out["file_url"] = part.FileURL
	}
	if part.FileID != "" {
		out["file_id"] = part.FileID
	}
	if part.Filename != "" {
		out["filename"] = part.Filename
	} else if partType == "input_file" && part.FileData != "" {
		out["filename"] = responsesDefaultFilename(part)
	}
	if part.FileType != "" && partType != "input_file" {
		out["file_type"] = part.FileType
	}
	if part.Data != "" {
		out["data"] = part.Data
	}
	if part.MimeType != "" && partType != "input_file" {
		out["mime_type"] = part.MimeType
	}
	if part.Refusal != "" {
		out["refusal"] = part.Refusal
	}
	return out
}

func responsesDefaultFilename(part schema.ContentPart) string {
	mimeType := firstNonEmptyString(part.FileType, part.MimeType)
	switch mimeType {
	case "application/pdf":
		return "input.pdf"
	case "text/plain":
		return "input.txt"
	case "text/csv":
		return "input.csv"
	case "application/json":
		return "input.json"
	case "text/markdown":
		return "input.md"
	default:
		return "input.bin"
	}
}

func init() {
	registerRequestIRParser(FormatOpenAIResponses, parseOpenAIResponsesRequestIR)
	registerRequestIREmitter(FormatOpenAIResponses, emitOpenAIResponsesRequestIR)
}
