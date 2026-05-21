package gemini

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// geminiResponseToInternal parses a Gemini generateContent response into InternalResponse.
func geminiResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse gemini response: %w", err)
	}
	if wrapped, ok := resp["response"].(map[string]interface{}); ok {
		resp = wrapped
	}

	ir := &provider.InternalResponse{
		ID:    "gemini-" + provider.RandomHex(16),
		Model: "gemini",
	}
	if modelVersion, ok := resp["modelVersion"].(string); ok && modelVersion != "" {
		ir.Model = modelVersion
	}

	// Parse candidates
	candidates, _ := resp["candidates"].([]interface{})
	if len(candidates) > 0 {
		cand, _ := candidates[0].(map[string]interface{})
		ir.Choices = []provider.InternalChoice{
			{
				Index: 0,
				Message: provider.InternalMessage{
					Role: "assistant",
				},
			},
		}

		// Finish reason mapping: Gemini → Internal
		if fr, ok := cand["finishReason"].(string); ok {
			ir.Choices[0].FinishReason = mapGeminiFinishReason(fr)
		}

		// Content parts
		if cont, ok := cand["content"].(map[string]interface{}); ok {
			parts, _ := cont["parts"].([]interface{})
			for _, partRaw := range parts {
				part, ok := partRaw.(map[string]interface{})
				if !ok {
					continue
				}
				// Text part → InternalContentPart
				if text, ok := part["text"].(string); ok {
					ir.Choices[0].Message.Content = append(ir.Choices[0].Message.Content, provider.InternalContentPart{
						Type: "text",
						Text: text,
					})
				}
				// functionCall part → InternalToolCall
				if fc, ok := part["functionCall"].(map[string]interface{}); ok {
					name, _ := fc["name"].(string)
					args := "{}"
					if a, err := json.Marshal(fc["args"]); err == nil {
						args = string(a)
					}
					ir.Choices[0].Message.ToolCalls = append(ir.Choices[0].Message.ToolCalls, provider.InternalToolCall{
						ID:        "fc_" + provider.RandomHex(8),
						Name:      name,
						Arguments: args,
					})
				}
			}
		}
	}

	// Usage metadata
	if um, ok := resp["usageMetadata"].(map[string]interface{}); ok {
		ir.Usage = provider.InternalUsage{
			PromptTokens:     provider.ToInt(um["promptTokenCount"]),
			CompletionTokens: provider.ToInt(um["candidatesTokenCount"]),
		}
	}

	return ir, nil
}

func geminiCodeAssistResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	return geminiResponseToInternal(body)
}

// internalToGeminiResponse serializes an InternalResponse into Gemini generateContent response format.
func internalToGeminiResponse(resp *provider.InternalResponse) ([]byte, error) {
	// Build candidates
	cand := map[string]interface{}{}

	// Finish reason mapping: Internal → Gemini (guard against empty Choices)
	if len(resp.Choices) > 0 {
		cand["finishReason"] = mapInternalFinishReason(resp.Choices[0].FinishReason)
	}

	// Content parts with role
	parts := []interface{}{}
	if len(resp.Choices) > 0 {
		for _, cp := range resp.Choices[0].Message.Content {
			if cp.Type == "text" {
				parts = append(parts, map[string]interface{}{"text": cp.Text})
			}
		}
		// Tool calls → functionCall parts (reverse args: JSON string → object)
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			args := map[string]interface{}{}
			if tc.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
			}
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": tc.Name,
					"args": args,
				},
			})
		}
	}

	if len(parts) == 0 {
		parts = []interface{}{map[string]interface{}{"text": ""}}
	}
	cand["content"] = map[string]interface{}{
		"parts": parts,
		"role":  "model",
	}

	// Usage metadata
	usage := map[string]interface{}{
		"promptTokenCount":     resp.Usage.PromptTokens,
		"candidatesTokenCount": resp.Usage.CompletionTokens,
		"totalTokenCount":      resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
	}

	result := map[string]interface{}{
		"candidates":    []interface{}{cand},
		"usageMetadata": usage,
	}

	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini response: %w", err)
	}
	return b, nil
}

// mapInternalFinishReason maps Internal finish reasons to Gemini finish reasons.
func mapInternalFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}
