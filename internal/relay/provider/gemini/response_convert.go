package gemini

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// geminiResponseToInternal parses a Gemini generateContent API response into InternalResponse.
func geminiResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &resp); err != nil {
		return nil, fmt.Errorf("parse gemini response: %w", err)
	}
	if wrapped, exists := resp["response"]; exists {
		switch v := wrapped.(type) {
		case map[string]interface{}:
			resp = v
		case []interface{}:
			if len(v) == 0 {
				return nil, fmt.Errorf("gemini response wrapper array is empty")
			}
			first, ok := v[0].(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini response wrapper array entries must be objects")
			}
			resp = first
		default:
			return nil, fmt.Errorf("gemini response wrapper must be an object or array")
		}
	}

	ir := &provider.InternalResponse{
		ID:    "gemini-" + provider.RandomHex(16),
		Model: "gemini",
	}
	if modelVersion, ok := resp["modelVersion"].(string); ok && modelVersion != "" {
		ir.Model = modelVersion
	}

	// Parse candidates
	candidatesRaw, exists := resp["candidates"]
	candidates, _ := candidatesRaw.([]interface{})
	if exists && candidates == nil {
		return nil, fmt.Errorf("gemini response candidates must be an array")
	}
	if len(candidates) > 0 {
		cand, ok := candidates[0].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini response candidates entries must be objects")
		}
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
		if contRaw, exists := cand["content"]; exists {
			cont, ok := contRaw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini response candidate content must be an object")
			}
			partsRaw, exists := cont["parts"]
			parts, _ := partsRaw.([]interface{})
			if exists && parts == nil {
				return nil, fmt.Errorf("gemini response candidate content parts must be an array")
			}
			for _, partRaw := range parts {
				part, ok := partRaw.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("gemini response parts entries must be objects")
				}
				if err := validateGeminiPartKeys(part); err != nil {
					return nil, err
				}
				// Text part → InternalContentPart
				if text, ok := part["text"].(string); ok {
					ir.Choices[0].Message.Content = append(ir.Choices[0].Message.Content, provider.InternalContentPart{
						Type: "text",
						Text: text,
					})
				} else if _, exists := part["text"]; exists {
					return nil, fmt.Errorf("gemini response text part requires string text")
				}
				// functionCall part → InternalToolCall
				if fc, ok := part["functionCall"].(map[string]interface{}); ok {
					if err := validateAllowedKeys(fc, "gemini response functionCall", "name", "args"); err != nil {
						return nil, err
					}
					name, _ := fc["name"].(string)
					if name == "" {
						return nil, fmt.Errorf("gemini response functionCall requires name")
					}
					args := "{}"
					if argsVal, exists := fc["args"]; exists {
						a, err := json.Marshal(argsVal)
						if err != nil {
							return nil, fmt.Errorf("gemini response functionCall args must be JSON-serializable: %w", err)
						}
						args = string(a)
					}
					ir.Choices[0].Message.ToolCalls = append(ir.Choices[0].Message.ToolCalls, provider.InternalToolCall{
						ID:        "fc_" + provider.RandomHex(8),
						Name:      name,
						Arguments: args,
					})
				} else if _, exists := part["functionCall"]; exists {
					return nil, fmt.Errorf("gemini response functionCall part requires object functionCall")
				}
				if _, hasText := part["text"]; !hasText {
					if _, hasCall := part["functionCall"]; !hasCall {
						return nil, fmt.Errorf("gemini response part cannot be converted to non-gemini downstream formats")
					}
				}
			}
		}
	}

	// Usage metadata
	if um, ok := resp["usageMetadata"].(map[string]interface{}); ok {
		usage := provider.InternalUsage{
			PromptTokens:     provider.ToInt(um["promptTokenCount"]),
			CompletionTokens: provider.ToInt(um["candidatesTokenCount"]),
		}
		if cached, ok := um["cachedContentTokenCount"]; ok {
			usage.CacheReadInputTokens = provider.ToInt(cached)
		}
		ir.Usage = usage
	}

	return ir, nil
}

func geminiCodeAssistResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	return geminiResponseToInternal(body)
}

// internalToGeminiResponse serializes an InternalResponse into Gemini generateContent API response format.
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
			var args json.RawMessage = []byte("{}")
			if tc.Arguments != "" {
				if !json.Valid([]byte(tc.Arguments)) {
					return nil, fmt.Errorf("internal tool call arguments for %q are not valid JSON", tc.Name)
				}
				args = json.RawMessage(tc.Arguments)
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
	if resp.Usage.CacheReadInputTokens > 0 {
		usage["cachedContentTokenCount"] = resp.Usage.CacheReadInputTokens
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

// ResponseToInternal exposes Gemini response parsing for providers with Gemini-shaped responses.
func ResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	return geminiResponseToInternal(body)
}
