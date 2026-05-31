package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// ParseGeminiResponse converts Gemini API response to adapterResponse.
func ParseGeminiResponse(body []byte) (*adapterResponse, error) {
	var wrapped struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Response) > 0 {
		body = wrapped.Response
	}

	var resp schema.GeminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini response: %w", err)
	}

	ir := &adapterResponse{
		ID:      "", // Gemini doesn't have an ID field
		Model:   resp.ModelVersion,
		Choices: make([]adapterChoice, 0, len(resp.Candidates)),
		Usage:   schema.Usage{},
		Raw:     body, // Preserve raw for same-format passthrough
	}

	// Convert usage
	if resp.UsageMetadata != nil {
		ir.Usage.PromptTokens = resp.UsageMetadata.PromptTokenCount
		ir.Usage.CompletionTokens = resp.UsageMetadata.CandidatesTokenCount
		ir.Usage.TotalTokens = resp.UsageMetadata.TotalTokenCount
		if ir.Usage.TotalTokens == 0 {
			ir.Usage.TotalTokens = ir.Usage.PromptTokens + ir.Usage.CompletionTokens
		}
		ir.Usage.CacheReadInputTokens = resp.UsageMetadata.CachedContentTokenCount
		if resp.UsageMetadata.ThoughtsTokenCount > 0 {
			if ir.Usage.CompletionTokensDetails == nil {
				ir.Usage.CompletionTokensDetails = map[string]interface{}{}
			}
			ir.Usage.CompletionTokensDetails["reasoning_tokens"] = resp.UsageMetadata.ThoughtsTokenCount
		}
	}

	// Convert candidates to choices
	for _, cand := range resp.Candidates {
		choice := adapterChoice{
			Index:        cand.Index,
			FinishReason: mapGeminiFinishReason(cand.FinishReason),
		}

		if cand.Content != nil {
			choice.Role = cand.Content.Role

			// Convert parts to content
			for _, part := range cand.Content.Parts {
				switch {
				case part.Text != "" && part.Thought:
					extra := map[string]json.RawMessage{}
					if part.ThoughtSignature != "" {
						extra = setRawString(extra, reasoningExtraThoughtSignature, part.ThoughtSignature)
					}
					appendChoiceReasoningItem(&choice, schema.ContentPart{
						Type:  "thinking",
						Text:  part.Text,
						Extra: extra,
					}, rawJSON(part))
				case part.Text != "":
					appendChoiceContentItem(&choice, schema.ContentPart{
						Type: "text",
						Text: part.Text,
					}, rawJSON(part))
				case part.InlineData != nil:
					dataURI := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data)
					appendChoiceContentItem(&choice, schema.ContentPart{
						Type:     "image_url",
						ImageURL: &dataURI,
					}, rawJSON(part))
				case part.FunctionCall != nil:
					appendChoiceToolCallItem(&choice, schema.ToolCall{
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
					}, rawJSON(part))
				case part.FunctionResponse != nil:
					// FunctionResponse is handled in the next turn
				case part.ThoughtSignature != "":
					appendChoiceReasoningItem(&choice, reasoningPartWithExtra("", map[string]json.RawMessage{
						reasoningExtraThoughtSignature: json.RawMessage(fmt.Sprintf(`%q`, part.ThoughtSignature)),
					}), rawJSON(part))
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

// mapGeminiResponseFinishReason converts internal finish_reason to Gemini format.
func mapGeminiResponseFinishReason(fr string) string {
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

// EmitGeminiResponse converts adapterResponse to Gemini API response.
func EmitGeminiResponse(ir *adapterResponse) ([]byte, error) {
	resp := make(map[string]interface{})

	// Convert choices to candidates
	candidates := make([]map[string]interface{}, 0, len(ir.Choices))
	for _, choice := range ir.Choices {
		cand := map[string]interface{}{
			"index":        choice.Index,
			"finishReason": mapGeminiResponseFinishReason(choice.FinishReason),
		}

		items := canonicalChoiceItems(choice)
		if len(items) > 0 {
			parts := make([]map[string]interface{}, 0)
			for _, item := range items {
				switch item.Kind {
				case contentItemKindReasoning:
					rc := item.Content
					sig := reasoningOpaqueSignature([]schema.ContentPart{rc})
					if rc.Text == "" && sig == "" {
						continue
					}
					part := map[string]interface{}{}
					if rc.Text != "" {
						part["text"] = rc.Text
						part["thought"] = true
					}
					if sig != "" {
						part["thoughtSignature"] = sig
					}
					parts = append(parts, part)
				case contentItemKindContent:
					c := item.Content
					part := map[string]interface{}{"type": c.Type}
					if c.Text != "" {
						part["text"] = c.Text
					}
					if c.ImageURL != nil {
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
				case contentItemKindToolCall:
					tc := item.ToolCall
					name := tc.Name
					if name == "" {
						name = tc.Function.Name
					}
					parts = append(parts, map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": name,
							"args": jsonArgumentValue(tc.Function.Arguments),
						},
					})
				case "refusal":
					if item.Content.Refusal != "" {
						parts = append(parts, map[string]interface{}{"text": item.Content.Refusal})
					}
				}
			}
			if len(parts) > 0 {
				cand["content"] = map[string]interface{}{
					"role":  choice.Role,
					"parts": parts,
				}
			}
		}

		candidates = append(candidates, cand)
	}
	resp["candidates"] = candidates

	// Convert usage
	if ir.Usage.TotalTokens > 0 || ir.Usage.PromptTokens > 0 {
		usage := map[string]interface{}{
			"promptTokenCount":     ir.Usage.PromptTokens,
			"candidatesTokenCount": ir.Usage.CompletionTokens,
			"totalTokenCount":      ir.Usage.TotalTokens,
		}
		if reasoningTokens := usageDetailInt(ir.Usage.CompletionTokensDetails, "reasoning_tokens"); reasoningTokens > 0 {
			usage["thoughtsTokenCount"] = reasoningTokens
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

// ParseGeminiCLIResponse extracts adapterResponse from Gemini CLI envelope.
func ParseGeminiCLIResponse(body []byte) (*adapterResponse, error) {
	var env schema.GeminiCLIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini CLI response: %w", err)
	}

	// Convert inner Gemini response to adapterResponse
	innerBody, err := json.Marshal(env.Response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inner Gemini response: %w", err)
	}

	ir, err := ParseGeminiResponse(innerBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert inner Gemini response: %w", err)
	}

	return ir, nil
}

// EmitGeminiCLIResponse wraps adapterResponse in Gemini CLI envelope.
func EmitGeminiCLIResponse(ir *adapterResponse) ([]byte, error) {
	// First convert to Gemini format
	innerBody, err := EmitGeminiResponse(ir)
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
	RegisterResponseParser(FormatGemini, ParseGeminiResponse)
	RegisterResponseEmitter(FormatGemini, EmitGeminiResponse)
	RegisterResponseParser(FormatGeminiCLI, ParseGeminiCLIResponse)
	RegisterResponseEmitter(FormatGeminiCLI, EmitGeminiCLIResponse)
}
