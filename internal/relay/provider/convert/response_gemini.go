package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// GeminiResponseToInternal converts Gemini API response to InternalResponse.
func GeminiResponseToInternal(body []byte) (*InternalResponse, error) {
	var resp schema.GeminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini response: %w", err)
	}

	ir := &InternalResponse{
		ID:      "", // Gemini doesn't have an ID field
		Model:   resp.ModelVersion,
		Choices: make([]InternalChoice, 0, len(resp.Candidates)),
		Usage:   schema.Usage{},
		Raw:     body, // Preserve raw for same-format passthrough
	}

	// Convert usage
	if resp.UsageMetadata != nil {
		ir.Usage.PromptTokens = resp.UsageMetadata.PromptTokenCount
		ir.Usage.CompletionTokens = resp.UsageMetadata.CandidatesTokenCount
		ir.Usage.TotalTokens = resp.UsageMetadata.TotalTokenCount
		ir.Usage.CacheCreationInputTokens = resp.UsageMetadata.CachedContentTokenCount
	}

	// Convert candidates to choices
	for _, cand := range resp.Candidates {
		choice := InternalChoice{
			Index:        cand.Index,
			FinishReason: mapGeminiFinishReason(cand.FinishReason),
		}

		if cand.Content != nil {
			choice.Role = cand.Content.Role

			// Convert parts to content
			for _, part := range cand.Content.Parts {
				switch {
				case part.Text != "":
					choice.Content = append(choice.Content, schema.ContentPart{
						Type: "text",
						Text: part.Text,
					})
				case part.InlineData != nil:
					dataURI := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data)
					choice.Content = append(choice.Content, schema.ContentPart{
						Type:     "image_url",
						ImageURL: &dataURI,
					})
				case part.FunctionCall != nil:
					choice.ToolCalls = append(choice.ToolCalls, schema.ToolCall{
						ID:   "", // Gemini doesn't provide ID for function calls
						Type: "function",
						Name: part.FunctionCall.Name,
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      part.FunctionCall.Name,
							Arguments: string(part.FunctionCall.Args),
						},
					})
				case part.FunctionResponse != nil:
					// FunctionResponse is handled in the next turn
				}
			}
		}

		ir.Choices = append(ir.Choices, choice)
	}

	return ir, nil
}

// mapGeminiFinishReason converts Gemini finishReason to internal format.
func mapGeminiFinishReason(fr string) string {
	switch fr {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "safety"
	case "RECITATION":
		return "recitation"
	case "OTHER":
		return "other"
	default:
		return fr
	}
}

// mapInternalToGeminiFinishReason converts internal finish_reason to Gemini format.
func mapInternalToGeminiFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "STOP"
	case "max_tokens":
		return "MAX_TOKENS"
	case "safety":
		return "SAFETY"
	case "recitation":
		return "RECITATION"
	case "other":
		return "OTHER"
	default:
		return fr
	}
}

// InternalToGeminiResponse converts InternalResponse to Gemini API response.
func InternalToGeminiResponse(ir *InternalResponse) ([]byte, error) {
	resp := make(map[string]interface{})

	// Convert choices to candidates
	candidates := make([]map[string]interface{}, 0, len(ir.Choices))
	for _, choice := range ir.Choices {
		cand := map[string]interface{}{
			"index":        choice.Index,
			"finishReason": mapInternalToGeminiFinishReason(choice.FinishReason),
		}

		// Convert content to parts
		if len(choice.Content) > 0 || len(choice.ToolCalls) > 0 {
			parts := make([]map[string]interface{}, 0)

			for _, c := range choice.Content {
				part := map[string]interface{}{"type": c.Type}
				if c.Text != "" {
					part["text"] = c.Text
				}
				if c.ImageURL != nil {
					// Parse data URI
					dataURI := *c.ImageURL
					mimeType := "image/png"
					data := dataURI
					if len(dataURI) > 5 && dataURI[:5] == "data:" {
						endIdx := len(dataURI)
						for i := 5; i < len(dataURI); i++ {
							if dataURI[i] == ';' || dataURI[i] == ',' {
								endIdx = i
								break
							}
						}
						mimeType = dataURI[5:endIdx]
						if endIdx < len(dataURI) && dataURI[endIdx] == ';' {
							for i := endIdx + 1; i < len(dataURI); i++ {
								if dataURI[i] == ',' {
									data = dataURI[i+1:]
									break
								}
							}
						}
					}
					part["inlineData"] = map[string]string{
						"mimeType": mimeType,
						"data":     data,
					}
				}
				parts = append(parts, part)
			}

			// Add tool calls
			for _, tc := range choice.ToolCalls {
				parts = append(parts, map[string]interface{}{
					"functionCall": map[string]interface{}{
						"name": tc.Name,
						"args": tc.Function.Arguments,
					},
				})
			}

			cand["content"] = map[string]interface{}{
				"role":  choice.Role,
				"parts": parts,
			}
		}

		candidates = append(candidates, cand)
	}
	resp["candidates"] = candidates

	// Convert usage
	if ir.Usage.TotalTokens > 0 || ir.Usage.PromptTokens > 0 {
		usage := map[string]interface{}{
			"promptTokenCount":      ir.Usage.PromptTokens,
			"candidatesTokenCount":  ir.Usage.CompletionTokens,
			"totalTokenCount":       ir.Usage.TotalTokens,
		}
		if ir.Usage.CacheCreationInputTokens > 0 {
			usage["cachedContentTokenCount"] = ir.Usage.CacheCreationInputTokens
		}
		resp["usageMetadata"] = usage
	}

	// If Raw is present, preserve extra fields
	if len(ir.Raw) > 0 {
		var rawMap map[string]json.RawMessage
		if json.Unmarshal(ir.Raw, &rawMap) == nil {
			for k, v := range rawMap {
				switch k {
				case "candidates", "usageMetadata", "modelVersion":
					// Skip standard fields
				default:
					resp[k] = v
				}
			}
		}
	}

	return json.Marshal(resp)
}

// GeminiCLIResponseToInternal extracts InternalResponse from Gemini CLI envelope.
func GeminiCLIResponseToInternal(body []byte) (*InternalResponse, error) {
	var env schema.GeminiCLIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini CLI response: %w", err)
	}

	// Convert inner Gemini response to InternalResponse
	innerBody, err := json.Marshal(env.Response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inner Gemini response: %w", err)
	}

	ir, err := GeminiResponseToInternal(innerBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert inner Gemini response: %w", err)
	}

	return ir, nil
}

// InternalToGeminiCLIResponse wraps InternalResponse in Gemini CLI envelope.
func InternalToGeminiCLIResponse(ir *InternalResponse) ([]byte, error) {
	// First convert to Gemini format
	innerBody, err := InternalToGeminiResponse(ir)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to Gemini format: %w", err)
	}

	// Parse the Gemini response
	var geminiResp schema.GeminiResponse
	if err := json.Unmarshal(innerBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini response: %w", err)
	}

	// Build the CLI envelope
	cliResp := schema.GeminiCLIResponse{
		Response: geminiResp,
	}

	return json.Marshal(cliResp)
}

func init() {
	RegisterToResponseInternal(FormatGemini, GeminiResponseToInternal)
	RegisterFromResponseInternal(FormatGemini, InternalToGeminiResponse)
	RegisterToResponseInternal(FormatGeminiCLI, GeminiCLIResponseToInternal)
	RegisterFromResponseInternal(FormatGeminiCLI, InternalToGeminiCLIResponse)
}