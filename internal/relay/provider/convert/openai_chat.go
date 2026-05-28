package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// OpenAIChatToInternal converts OpenAI Chat Completions request to InternalRequest.
func OpenAIChatToInternal(body []byte) (*InternalRequest, error) {
	var req schema.OpenAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Chat request: %w", err)
	}

	ir := &InternalRequest{
		Model:        req.Model,
		Stream:       req.Stream,
		SourceFormat: FormatOpenAIChatCompletions,
		Extra:        make(map[string]json.RawMessage),
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
	}

	// Extract system/developer messages into Instructions
	var instructions []string
	var messages []InternalMessage

	for _, msg := range req.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			text := msg.Content.ExtractText()
			if text != "" {
				instructions = append(instructions, text)
			}
			continue
		}

		// Convert message content
		content := msg.Content.Parts
		if content == nil && msg.Content.Text != nil {
			content = []schema.ContentPart{{Type: "text", Text: *msg.Content.Text}}
		}

		internalMsg := InternalMessage{
			Role:    msg.Role,
			Content: content,
			Name:    msg.Name,
		}

		// Convert tool calls
		if len(msg.ToolCalls) > 0 {
			internalMsg.ToolCalls = make([]schema.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				internalMsg.ToolCalls[i] = schema.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Name: tc.Function.Name,
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

		// Convert tool result (role=tool)
		if msg.Role == "tool" {
			internalMsg.ToolResult = &schema.ToolResult{
				ToolCallID: msg.ToolCallID,
				Content:    msg.Content.ExtractText(),
			}
		}

		messages = append(messages, internalMsg)
	}

	// Set Instructions if any system/developer messages existed
	if len(instructions) > 0 {
		inst := joinNonEmpty(instructions, "\n\n")
		ir.Instructions = &inst
	}

	ir.Messages = messages

	// Generation parameters
	if req.MaxTokens != nil {
		ir.MaxTokens = req.MaxTokens
	}
	if req.MaxCompletionTokens != nil {
		// Prefer max_completion_tokens if set
		ir.MaxTokens = req.MaxCompletionTokens
	}
	if req.Temperature != nil {
		ir.Temperature = req.Temperature
	}
	if req.TopP != nil {
		ir.TopP = req.TopP
	}
	if req.Stop != nil {
		// Stop can be string or array
		var stopWords []string
		if err := json.Unmarshal(req.Stop, &stopWords); err == nil {
			ir.StopWords = stopWords
		} else {
			var stopStr string
			if err := json.Unmarshal(req.Stop, &stopStr); err == nil {
				ir.StopWords = []string{stopStr}
			}
		}
	}
	if req.FrequencyPenalty != nil {
		ir.FrequencyPenalty = req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		ir.PresencePenalty = req.PresencePenalty
	}
	if req.N != nil {
		ir.N = req.N
	}
	if req.Seed != nil {
		seed := int64(*req.Seed)
		ir.Seed = &seed
	}
	if req.LogProbs {
		ir.LogProbs = &req.LogProbs
	}
	if req.TopLogProbs != nil {
		ir.TopLogProbs = req.TopLogProbs
	}
	if req.ResponseFormat != nil {
		ir.ResponseFormat = req.ResponseFormat
	}
	if req.LogitBias != nil {
		ir.LogitBias = req.LogitBias
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
	if req.ReasoningEffort != "" {
		ir.Reasoning = json.RawMessage(fmt.Sprintf(`{"effort":%q}`, req.ReasoningEffort))
	}
	if req.Tools != nil {
		ir.Tools = req.Tools
	}
	if req.ToolChoice != nil {
		ir.ToolChoice = req.ToolChoice
	}

	return ir, nil
}

// InternalToOpenAIChat converts InternalRequest to OpenAI Chat Completions request.
func InternalToOpenAIChat(ir *InternalRequest) ([]byte, error) {
	req := schema.OpenAIChatRequest{
		Model:  ir.Model,
		Stream: ir.Stream,
		Extra:  make(map[string]json.RawMessage),
	}

	// Copy Extra fields
	for k, v := range ir.Extra {
		req.Extra[k] = v
	}

	// Prepend Instructions as system message if present
	var messages []schema.ChatMessage
	if ir.Instructions != nil && *ir.Instructions != "" {
		messages = append(messages, schema.ChatMessage{
			Role:    "system",
			Content: schema.NewTextContent(*ir.Instructions),
		})
	}

	// Convert InternalMessages to ChatMessages
	for _, msg := range ir.Messages {
		chatMsg := schema.ChatMessage{
			Role:    msg.Role,
			Name:    msg.Name,
			Content: schema.NewPartsContent(msg.Content...),
		}

		// Convert tool calls
		if len(msg.ToolCalls) > 0 {
			chatMsg.ToolCalls = make([]schema.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				chatMsg.ToolCalls[i] = schema.ToolCall{
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

		// Convert tool result
		if msg.ToolResult != nil {
			chatMsg.ToolCallID = msg.ToolResult.ToolCallID
		}

		messages = append(messages, chatMsg)
	}
	req.Messages = messages

	// Generation parameters
	if ir.MaxTokens != nil {
		req.MaxTokens = ir.MaxTokens
	}
	if ir.Temperature != nil {
		req.Temperature = ir.Temperature
	}
	if ir.TopP != nil {
		req.TopP = ir.TopP
	}
	if len(ir.StopWords) > 0 {
		if len(ir.StopWords) == 1 {
			stop, _ := json.Marshal(ir.StopWords[0])
			req.Stop = stop
		} else {
			stop, _ := json.Marshal(ir.StopWords)
			req.Stop = stop
		}
	}
	if ir.FrequencyPenalty != nil {
		req.FrequencyPenalty = ir.FrequencyPenalty
	}
	if ir.PresencePenalty != nil {
		req.PresencePenalty = ir.PresencePenalty
	}
	if ir.N != nil {
		req.N = ir.N
	}
	if ir.Seed != nil {
		seed := int(*ir.Seed)
		req.Seed = &seed
	}
	if ir.LogProbs != nil {
		req.LogProbs = *ir.LogProbs
	}
	if ir.TopLogProbs != nil {
		req.TopLogProbs = ir.TopLogProbs
	}
	if ir.ResponseFormat != nil {
		req.ResponseFormat = ir.ResponseFormat
	}
	if ir.LogitBias != nil {
		req.LogitBias = ir.LogitBias
	}
	if ir.ParallelToolCalls != nil {
		req.ParallelToolCalls = *ir.ParallelToolCalls
	}
	if ir.ServiceTier != "" {
		req.ServiceTier = ir.ServiceTier
	}
	if ir.Store != nil {
		req.Store = *ir.Store
	}
	if ir.Tools != nil {
		req.Tools = ir.Tools
	}
	if ir.ToolChoice != nil {
		req.ToolChoice = ir.ToolChoice
	}

	return json.Marshal(req)
}

// joinNonEmpty joins non-empty strings with the given separator.
func joinNonEmpty(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if s == "" {
			continue
		}
		if i > 0 && result != "" {
			result += sep
		}
		result += s
	}
	return result
}

func init() {
	RegisterToInternal(FormatOpenAIChatCompletions, OpenAIChatToInternal)
	RegisterFromInternal(FormatOpenAIChatCompletions, InternalToOpenAIChat)
}
