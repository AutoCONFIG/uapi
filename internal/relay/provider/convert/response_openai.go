package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// OpenAIChatResponseToInternal converts OpenAI Chat Completions response to InternalResponse.
func OpenAIChatResponseToInternal(body []byte) (*InternalResponse, error) {
	var resp schema.OpenAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Chat response: %w", err)
	}

	ir := &InternalResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: make([]InternalChoice, 0, len(resp.Choices)),
		Usage:   schema.Usage{},
		Raw:     body, // Preserve raw for same-format passthrough
	}

	// Convert usage
	if resp.Usage != nil {
		ir.Usage.PromptTokens = resp.Usage.PromptTokens
		ir.Usage.CompletionTokens = resp.Usage.CompletionTokens
		ir.Usage.TotalTokens = resp.Usage.TotalTokens
		cachedTokens := usageDetailInt(resp.Usage.PromptTokensDetails, "cached_tokens")
		ir.Usage.CacheReadInputTokens = cachedTokens
	}

	// Convert choices
	for _, choice := range resp.Choices {
		internalChoice := InternalChoice{
			Index:        choice.Index,
			Role:         choice.Message.Role,
			FinishReason: mapOpenAIChatFinishReason(choice.FinishReason),
		}

		// Convert content
		if !choice.Message.Content.IsEmpty() {
			internalChoice.Content = choice.Message.Content.Parts
		}
		if internalChoice.Content == nil && choice.Message.Content.Text != nil {
			internalChoice.Content = []schema.ContentPart{
				{Type: "text", Text: *choice.Message.Content.Text},
			}
		}

		// Convert tool calls
		if len(choice.Message.ToolCalls) > 0 {
			internalChoice.ToolCalls = make([]schema.ToolCall, len(choice.Message.ToolCalls))
			for i, tc := range choice.Message.ToolCalls {
				internalChoice.ToolCalls[i] = schema.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Name: tc.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		// Handle refusal
		if choice.Message.Refusal != "" {
			internalChoice.Refusal = choice.Message.Refusal
		}

		ir.Choices = append(ir.Choices, internalChoice)
	}

	return ir, nil
}

func usageDetailInt(details map[string]interface{}, key string) int {
	if details == nil {
		return 0
	}
	switch v := details[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

// mapOpenAIChatFinishReason converts OpenAI finish_reason to internal format.
func mapOpenAIChatFinishReason(fr string) string {
	switch fr {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "content_filter"
	case "function_call":
		return "tool_use"
	default:
		return fr
	}
}

// mapInternalToOpenAIChatFinishReason converts internal finish_reason to OpenAI format.
func mapInternalToOpenAIChatFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	default:
		return fr
	}
}

// InternalToOpenAIChatResponse converts InternalResponse to OpenAI Chat Completions response.
func InternalToOpenAIChatResponse(ir *InternalResponse) ([]byte, error) {
	resp := schema.OpenAIChatResponse{
		ID:      ir.ID,
		Object:  "chat.completion",
		Created: 0, // Will be set by caller if needed
		Model:   ir.Model,
		Choices: make([]schema.ChatChoice, 0, len(ir.Choices)),
	}

	// Convert usage
	if ir.Usage.TotalTokens > 0 {
		resp.Usage = &schema.Usage{
			PromptTokens:        ir.Usage.PromptTokens,
			CompletionTokens:    ir.Usage.CompletionTokens,
			TotalTokens:         ir.Usage.TotalTokens,
			PromptTokensDetails: map[string]interface{}{},
		}
		if ir.Usage.CacheCreationInputTokens > 0 {
			resp.Usage.PromptTokensDetails["cached_tokens"] = ir.Usage.CacheCreationInputTokens
		}
		if ir.Usage.CacheReadInputTokens > 0 {
			if resp.Usage.PromptTokensDetails == nil {
				resp.Usage.PromptTokensDetails = map[string]interface{}{}
			}
			resp.Usage.PromptTokensDetails["cached_tokens"] = ir.Usage.CacheReadInputTokens
		}
	}

	// Convert choices
	for _, choice := range ir.Choices {
		chatChoice := schema.ChatChoice{
			Index:        choice.Index,
			FinishReason: mapInternalToOpenAIChatFinishReason(choice.FinishReason),
			Message: schema.ChatMessage{
				Role: choice.Role,
			},
		}

		// Convert content
		if len(choice.Content) > 0 {
			if len(choice.Content) == 1 && choice.Content[0].Type == "text" {
				chatChoice.Message.Content = schema.NewTextContent(choice.Content[0].Text)
			} else {
				chatChoice.Message.Content = schema.NewPartsContent(choice.Content...)
			}
		}
		if len(choice.ReasoningContent) > 0 {
			if chatChoice.Message.Extra == nil {
				chatChoice.Message.Extra = make(map[string]json.RawMessage)
			}
			reasoning := contentPartsText(choice.ReasoningContent)
			if reasoning != "" {
				raw, _ := json.Marshal(reasoning)
				chatChoice.Message.Extra["reasoning_content"] = raw
				chatChoice.Message.Extra["reasoning"] = raw
			}
		}

		// Convert tool calls
		if len(choice.ToolCalls) > 0 {
			chatChoice.Message.ToolCalls = make([]schema.ToolCall, len(choice.ToolCalls))
			for i, tc := range choice.ToolCalls {
				chatChoice.Message.ToolCalls[i] = schema.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Name: tc.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		// Handle refusal
		if choice.Refusal != "" {
			chatChoice.Message.Refusal = choice.Refusal
		}

		resp.Choices = append(resp.Choices, chatChoice)
	}

	return json.Marshal(resp)
}

func contentPartsText(parts []schema.ContentPart) string {
	var out []string
	for _, part := range parts {
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

// OpenAIResponsesResponseToInternal converts OpenAI Responses API response to InternalResponse.
func OpenAIResponsesResponseToInternal(body []byte) (*InternalResponse, error) {
	var resp schema.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses response: %w", err)
	}

	ir := &InternalResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: make([]InternalChoice, 0),
		Usage:   schema.Usage{},
		Raw:     body, // Preserve raw for same-format passthrough
	}

	// Convert usage
	if resp.Usage != nil {
		ir.Usage.PromptTokens = resp.Usage.InputTokens
		ir.Usage.CompletionTokens = resp.Usage.OutputTokens
		ir.Usage.TotalTokens = resp.Usage.TotalTokens
	}

	// Convert output items
	for _, item := range resp.Output {
		choice := InternalChoice{
			Index: len(ir.Choices),
			Role:  item.Role,
		}

		switch item.Type {
		case "message":
			choice.Content = item.Content

		case "function_call":
			choice.ToolCalls = []schema.ToolCall{
				{
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
				},
			}
			choice.FinishReason = "tool_use"
		}

		// Map finish reason
		if item.Status == "completed" {
			choice.FinishReason = "end_turn"
		} else if item.Status == "incomplete" {
			choice.FinishReason = "max_tokens"
		}

		ir.Choices = append(ir.Choices, choice)
	}

	return ir, nil
}

// mapResponsesToInternalFinishReason converts Responses API status to internal finish reason.
func mapResponsesToInternalFinishReason(status string) string {
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	default:
		return status
	}
}

// InternalToOpenAIResponsesResponse converts InternalResponse to OpenAI Responses API response.
func InternalToOpenAIResponsesResponse(ir *InternalResponse) ([]byte, error) {
	resp := schema.OpenAIResponsesResponse{
		ID:        ir.ID,
		Object:    "response",
		CreatedAt: 0, // Will be set by caller if needed
		Model:     ir.Model,
		Output:    make([]schema.ResponsesOutputItem, 0, len(ir.Choices)),
	}

	// Convert usage
	if ir.Usage.TotalTokens > 0 {
		resp.Usage = &schema.ResponsesUsage{
			InputTokens:  ir.Usage.PromptTokens,
			OutputTokens: ir.Usage.CompletionTokens,
			TotalTokens:  ir.Usage.TotalTokens,
		}
	}

	// Convert choices to output items
	for _, choice := range ir.Choices {
		if len(choice.ToolCalls) == 0 {
			if contentItems := responsesContentOutputItems(choice); len(contentItems) > 0 {
				resp.Output = append(resp.Output, contentItems...)
				continue
			}
		}
		item := schema.ResponsesOutputItem{
			Type: "message",
			Role: choice.Role,
		}

		// Convert content
		if len(choice.Content) > 0 {
			item.Content = choice.Content
		}

		// Convert tool calls
		if len(choice.ToolCalls) > 0 {
			item.Type = "function_call"
			item.CallID = choice.ToolCalls[0].ID
			item.Name = choice.ToolCalls[0].Name
			item.Arguments = choice.ToolCalls[0].Function.Arguments
		}

		// Map finish reason to status
		switch choice.FinishReason {
		case "end_turn", "stop":
			item.Status = "completed"
		case "max_tokens", "length":
			item.Status = "incomplete"
		default:
			item.Status = choice.FinishReason
		}

		resp.Output = append(resp.Output, item)
	}

	// If Raw is present, preserve extra fields
	if len(ir.Raw) > 0 {
		var rawMap map[string]json.RawMessage
		if json.Unmarshal(ir.Raw, &rawMap) == nil {
			for k := range rawMap {
				switch k {
				case "id", "object", "created_at", "model", "output", "usage", "status":
					// Skip standard fields
				default:
					// Extra fields would be added here for passthrough
				}
			}
		}
	}

	return json.Marshal(resp)
}

func responsesContentOutputItems(choice InternalChoice) []schema.ResponsesOutputItem {
	out := make([]schema.ResponsesOutputItem, 0)
	pending := make([]schema.ContentPart, 0)
	flushMessage := func() {
		if len(pending) == 0 {
			return
		}
		out = append(out, schema.ResponsesOutputItem{
			Type:    "message",
			Role:    choice.Role,
			Content: pending,
			Status:  responsesStatusFromFinishReason(choice.FinishReason),
		})
		pending = nil
	}
	for idx, part := range choice.Content {
		if part.ImageURL == nil || *part.ImageURL == "" {
			pending = append(pending, part)
			continue
		}
		mimeType, b64, ok := splitDataURI(*part.ImageURL)
		if !ok {
			pending = append(pending, part)
			continue
		}
		flushMessage()
		format := imageOutputFormatFromMime(mimeType)
		resultRaw, _ := json.Marshal(b64)
		formatRaw, _ := json.Marshal(format)
		out = append(out, schema.ResponsesOutputItem{
			Type:   "image_generation_call",
			ID:     fmt.Sprintf("ig_%d", idx),
			Status: "completed",
			Extra: map[string]json.RawMessage{
				"result":        resultRaw,
				"output_format": formatRaw,
			},
		})
	}
	flushMessage()
	return out
}

func responsesStatusFromFinishReason(finishReason string) string {
	switch finishReason {
	case "end_turn", "stop":
		return "completed"
	case "max_tokens", "length":
		return "incomplete"
	default:
		return finishReason
	}
}

func splitDataURI(uri string) (mimeType, data string, ok bool) {
	if !strings.HasPrefix(uri, "data:") {
		return "", "", false
	}
	comma := strings.Index(uri, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := uri[len("data:"):comma]
	data = uri[comma+1:]
	if data == "" {
		return "", "", false
	}
	mimeType = "image/png"
	if semi := strings.Index(meta, ";"); semi >= 0 {
		if meta[:semi] != "" {
			mimeType = meta[:semi]
		}
	} else if meta != "" {
		mimeType = meta
	}
	return mimeType, data, true
}

func imageOutputFormatFromMime(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func init() {
	RegisterToResponseInternal(FormatOpenAIChatCompletions, OpenAIChatResponseToInternal)
	RegisterFromResponseInternal(FormatOpenAIChatCompletions, InternalToOpenAIChatResponse)
	RegisterToResponseInternal(FormatOpenAIResponses, OpenAIResponsesResponseToInternal)
	RegisterFromResponseInternal(FormatOpenAIResponses, InternalToOpenAIResponsesResponse)
}
