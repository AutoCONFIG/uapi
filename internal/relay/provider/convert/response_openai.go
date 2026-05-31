package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// ParseOpenAIChatResponse converts OpenAI Chat Completions response to adapterResponse.
func ParseOpenAIChatResponse(body []byte) (*adapterResponse, error) {
	var resp schema.OpenAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Chat response: %w", err)
	}

	ir := &adapterResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: make([]adapterChoice, 0, len(resp.Choices)),
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
		if resp.Usage.CompletionTokensDetails != nil {
			ir.Usage.CompletionTokensDetails = resp.Usage.CompletionTokensDetails
		}
	}

	// Convert choices
	for _, choice := range resp.Choices {
		internalChoice := adapterChoice{
			Index:        choice.Index,
			Role:         choice.Message.Role,
			FinishReason: mapOpenAIChatFinishReason(choice.FinishReason),
		}

		// Convert content
		if !choice.Message.Content.IsEmpty() {
			for _, part := range choice.Message.Content.Parts {
				appendChoiceContentItem(&internalChoice, part, rawJSON(part))
			}
		}
		if len(contentPartsFromItems(internalChoice.Items)) == 0 && choice.Message.Content.Text != nil {
			appendChoiceContentItem(&internalChoice, schema.ContentPart{Type: "text", Text: *choice.Message.Content.Text}, rawJSON(choice.Message.Content.Text))
		}
		for _, part := range reasoningPartsFromOpenAIChatExtra(choice.Message.Extra) {
			appendChoiceReasoningItem(&internalChoice, part, rawJSON(part))
		}

		// Convert tool calls
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				call := schema.ToolCall{
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
				appendChoiceToolCallItem(&internalChoice, call, rawJSON(tc))
			}
		}

		// Handle refusal
		if choice.Message.Refusal != "" {
			appendChoiceRefusalItem(&internalChoice, choice.Message.Refusal)
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

// mapOpenAIChatResponseFinishReason converts internal finish_reason to OpenAI format.
func mapOpenAIChatResponseFinishReason(fr string) string {
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

// EmitOpenAIChatResponse converts adapterResponse to OpenAI Chat Completions response.
func EmitOpenAIChatResponse(ir *adapterResponse) ([]byte, error) {
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
		if ir.Usage.CompletionTokensDetails != nil {
			resp.Usage.CompletionTokensDetails = ir.Usage.CompletionTokensDetails
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
			FinishReason: mapOpenAIChatResponseFinishReason(choice.FinishReason),
			Message: schema.ChatMessage{
				Role: choice.Role,
			},
		}

		items := canonicalChoiceItems(choice)
		content := contentPartsFromItems(items)
		reasoningContent := reasoningPartsFromItems(items)
		toolCalls := toolCallsFromItems(items)

		// Convert content
		if len(content) > 0 {
			if len(content) == 1 && content[0].Type == "text" {
				chatChoice.Message.Content = schema.NewTextContent(content[0].Text)
			} else {
				chatChoice.Message.Content = schema.NewPartsContent(content...)
			}
		}
		if len(reasoningContent) > 0 {
			if chatChoice.Message.Extra == nil {
				chatChoice.Message.Extra = make(map[string]json.RawMessage)
			}
			reasoning := contentPartsText(reasoningContent)
			if reasoning != "" {
				raw, _ := json.Marshal(reasoning)
				chatChoice.Message.Extra["reasoning_content"] = raw
				chatChoice.Message.Extra["reasoning"] = raw
			}
			if details := reasoningDetailsFromParts(reasoningContent); len(details) > 0 {
				raw, _ := json.Marshal(details)
				chatChoice.Message.Extra["reasoning_details"] = raw
			}
		}

		// Convert tool calls
		if len(toolCalls) > 0 {
			chatChoice.Message.ToolCalls = make([]schema.ToolCall, len(toolCalls))
			for i, tc := range toolCalls {
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

		for _, item := range items {
			if item.Kind == "refusal" && item.Content.Refusal != "" {
				chatChoice.Message.Refusal = item.Content.Refusal
				break
			}
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

// ParseOpenAIResponsesResponse converts OpenAI Responses API response to adapterResponse.
func ParseOpenAIResponsesResponse(body []byte) (*adapterResponse, error) {
	var resp schema.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses response: %w", err)
	}

	ir := &adapterResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: make([]adapterChoice, 0),
		Usage:   schema.Usage{},
		Raw:     body, // Preserve raw for same-format passthrough
	}

	// Convert usage
	if resp.Usage != nil {
		ir.Usage.PromptTokens = resp.Usage.InputTokens
		ir.Usage.CompletionTokens = resp.Usage.OutputTokens
		ir.Usage.TotalTokens = resp.Usage.TotalTokens
	}

	var pendingReasoning []adapterItem
	flushPendingReasoning := func() {
		if len(pendingReasoning) == 0 {
			return
		}
		choice := adapterChoice{Index: len(ir.Choices), Role: "assistant", FinishReason: "end_turn"}
		for _, item := range pendingReasoning {
			appendChoiceReasoningItem(&choice, item.Content, item.Raw)
		}
		ir.Choices = append(ir.Choices, choice)
		pendingReasoning = nil
	}

	// Convert output items. Responses can carry reasoning as a separate output
	// item immediately before the assistant message or tool call it belongs to.
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			choice := adapterChoice{
				Index:        len(ir.Choices),
				Role:         item.Role,
				FinishReason: mapResponsesStatusToFinishReason(item.Status),
			}
			for _, pending := range pendingReasoning {
				appendChoiceReasoningItem(&choice, pending.Content, pending.Raw)
			}
			for _, part := range item.Content {
				appendChoiceContentItem(&choice, part, rawJSON(part))
			}
			pendingReasoning = nil
			ir.Choices = append(ir.Choices, choice)

		case "function_call":
			choice := adapterChoice{
				Index:        len(ir.Choices),
				Role:         item.Role,
				FinishReason: "tool_use",
			}
			for _, pending := range pendingReasoning {
				appendChoiceReasoningItem(&choice, pending.Content, pending.Raw)
			}
			appendChoiceToolCallItem(&choice, schema.ToolCall{
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
			}, rawJSON(item))
			pendingReasoning = nil
			ir.Choices = append(ir.Choices, choice)

		case "reasoning":
			for _, part := range reasoningPartsFromResponsesExtra(item.Extra) {
				pendingReasoning = append(pendingReasoning, adapterItem{Kind: contentItemKindReasoning, Content: part, Raw: rawJSON(item)})
			}
		}
	}
	flushPendingReasoning()

	return ir, nil
}

// mapResponsesStatusToFinishReason converts Responses API status to internal finish reason.
func mapResponsesStatusToFinishReason(status string) string {
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	default:
		return status
	}
}

// EmitOpenAIResponsesResponse converts adapterResponse to OpenAI Responses API response.
func EmitOpenAIResponsesResponse(ir *adapterResponse) ([]byte, error) {
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
		resp.Output = append(resp.Output, responsesOutputItemsFromChoice(choice)...)
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

func responsesReasoningOutputItem(choice adapterChoice) schema.ResponsesOutputItem {
	reasoningContent := reasoningPartsFromItems(canonicalChoiceItems(choice))
	extra := make(map[string]json.RawMessage)
	if content := responsesReasoningContent(reasoningContent); len(content) > 0 {
		raw, _ := json.Marshal(content)
		extra["content"] = raw
	}
	if summary := responsesReasoningSummary(reasoningContent); len(summary) > 0 {
		raw, _ := json.Marshal(summary)
		extra["summary"] = raw
	}
	if encrypted := reasoningEncryptedContent(reasoningContent); encrypted != "" {
		raw, _ := json.Marshal(encrypted)
		extra[reasoningExtraEncryptedContent] = raw
	}
	return schema.ResponsesOutputItem{
		Type:   "reasoning",
		ID:     generateResponsesReasoningID(),
		Status: responsesStatusFromFinishReason(choice.FinishReason),
		Extra:  extra,
	}
}

func responsesReasoningContent(parts []schema.ContentPart) []map[string]interface{} {
	var content []map[string]interface{}
	for _, part := range parts {
		if typ := reasoningPartExtraString(part, reasoningExtraType); typ == reasoningDetailTypeSummary || typ == reasoningDetailTypeEncrypted {
			continue
		}
		if part.Text == "" && reasoningSignature([]schema.ContentPart{part}) == "" {
			continue
		}
		block := map[string]interface{}{
			"type": "reasoning",
		}
		if part.Text != "" {
			block["text"] = part.Text
		}
		if sig := reasoningSignature([]schema.ContentPart{part}); sig != "" {
			block["signature"] = sig
		}
		content = append(content, block)
	}
	return content
}

func responsesOutputItemsFromChoice(choice adapterChoice) []schema.ResponsesOutputItem {
	var out []schema.ResponsesOutputItem
	var pendingContent []schema.ContentPart
	status := responsesStatusFromFinishReason(choice.FinishReason)
	flushContent := func() {
		if len(pendingContent) == 0 {
			return
		}
		out = append(out, schema.ResponsesOutputItem{
			Type:    "message",
			Role:    choice.Role,
			Content: pendingContent,
			Status:  status,
		})
		pendingContent = nil
	}
	for idx, item := range canonicalChoiceItems(choice) {
		switch item.Kind {
		case contentItemKindReasoning:
			flushContent()
			reasoningChoice := choice
			reasoningChoice.Items = []adapterItem{item}
			out = append(out, responsesReasoningOutputItem(reasoningChoice))
		case contentItemKindContent:
			part := item.Content
			if part.ImageURL != nil && *part.ImageURL != "" {
				if mimeType, b64, ok := splitDataURI(*part.ImageURL); ok {
					flushContent()
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
					continue
				}
			}
			pendingContent = append(pendingContent, part)
		case contentItemKindToolCall:
			flushContent()
			name := item.ToolCall.Name
			if name == "" {
				name = item.ToolCall.Function.Name
			}
			out = append(out, schema.ResponsesOutputItem{
				Type:      "function_call",
				Role:      choice.Role,
				Status:    status,
				CallID:    item.ToolCall.ID,
				Name:      name,
				Arguments: item.ToolCall.Function.Arguments,
			})
		case "refusal":
			part := item.Content
			if part.Type == "" {
				part.Type = "refusal"
			}
			pendingContent = append(pendingContent, part)
		}
	}
	flushContent()
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
	RegisterResponseParser(FormatOpenAIChatCompletions, ParseOpenAIChatResponse)
	RegisterResponseEmitter(FormatOpenAIChatCompletions, EmitOpenAIChatResponse)
	RegisterResponseParser(FormatOpenAIResponses, ParseOpenAIResponsesResponse)
	RegisterResponseEmitter(FormatOpenAIResponses, EmitOpenAIResponsesResponse)
}
