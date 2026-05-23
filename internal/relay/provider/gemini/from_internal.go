package gemini

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// internalToGemini converts InternalRequest to Gemini API JSON.
func internalToGemini(req *provider.InternalRequest) ([]byte, error) {
	if err := rejectUnsupportedGeminiInputs(req); err != nil {
		return nil, err
	}
	gemReq := make(map[string]interface{})

	// Convert messages → contents
	var systemInstruction interface{}
	var contents []interface{}
	toolNamesByID := map[string]string{}

	for _, im := range req.Messages {
		switch im.Role {
		case "system":
			// System → systemInstruction
			parts, err := contentToGeminiParts(im.Content)
			if err != nil {
				return nil, err
			}
			if len(parts) > 0 {
				systemInstruction = map[string]interface{}{"parts": parts}
			}
		case "tool":
			// Tool result → functionResponse part
			if im.ToolResult != nil && im.ToolResult.Name == "" {
				if name := toolNamesByID[im.ToolResult.ToolCallID]; name != "" {
					im.ToolResult.Name = name
				}
			}
			contents = append(contents, buildGeminiToolResult(im))
		case "assistant":
			for _, tc := range im.ToolCalls {
				if tc.ID != "" && tc.Name != "" {
					toolNamesByID[tc.ID] = tc.Name
				}
			}
			msgs, err := buildGeminiAssistantMessage(im)
			if err != nil {
				return nil, err
			}
			contents = append(contents, msgs...)
		default:
			// user
			msg, err := buildGeminiUserMessage(im)
			if err != nil {
				return nil, err
			}
			contents = append(contents, msg)
		}
	}

	if systemInstruction != nil {
		gemReq["systemInstruction"] = systemInstruction
	}
	gemReq["contents"] = contents

	// generationConfig
	genConfig := make(map[string]interface{})
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	}
	if req.TopK != nil && *req.TopK > 0 {
		genConfig["topK"] = *req.TopK
	}
	if req.CandidateCount != nil && *req.CandidateCount > 0 {
		genConfig["candidateCount"] = *req.CandidateCount
	}
	if len(req.StopWords) > 0 {
		// Convert []string to []interface{}
		stopSeqs := make([]interface{}, len(req.StopWords))
		for i, s := range req.StopWords {
			stopSeqs[i] = s
		}
		genConfig["stopSequences"] = stopSeqs
	}
	if len(genConfig) > 0 {
		gemReq["generationConfig"] = genConfig
	}

	// SafetySettings
	if req.SafetySettings != nil {
		gemReq["safetySettings"] = req.SafetySettings
	}

	// Provider
	if req.Provider != "" {
		gemReq["provider"] = req.Provider
	}

	// Convert tools
	if len(req.Tools) > 0 {
		declarations := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			decl := map[string]interface{}{
				"name":        it.Name,
				"description": it.Description,
			}
			if it.Parameters != nil {
				decl["parameters"] = it.Parameters
			}
			declarations = append(declarations, decl)
		}
		gemReq["tools"] = []interface{}{
			map[string]interface{}{
				"functionDeclarations": declarations,
			},
		}
	}

	// ToolConfig → ToolChoice
	if req.ToolChoice != nil {
		gemReq["toolConfig"] = buildGeminiToolConfig(req.ToolChoice)
	}

	// Merge ExtraParams back into output for lossless round-tripping
	if len(req.ExtraParams) > 0 {
		for key, val := range req.ExtraParams {
			// Handle generationConfig.* prefixed keys: merge back into generationConfig
			if strings.HasPrefix(key, "generationConfig.") {
				gcKey := strings.TrimPrefix(key, "generationConfig.")
				var gc map[string]interface{}
				if existing, exists := gemReq["generationConfig"]; exists {
					gc, _ = existing.(map[string]interface{})
				}
				if gc == nil {
					gc = make(map[string]interface{})
				}
				gc[gcKey] = val
				gemReq["generationConfig"] = gc
			} else {
				// Direct field — do not overwrite explicitly set fields
				if _, exists := gemReq[key]; !exists {
					gemReq[key] = val
				}
			}
		}
	}

	return json.Marshal(gemReq)
}

func rejectUnsupportedGeminiInputs(req *provider.InternalRequest) error {
	for _, msg := range req.Messages {
		for _, part := range msg.Content {
			if part.Type == "image_url" && part.ImageURL != nil && !strings.HasPrefix(*part.ImageURL, "data:") {
				return fmt.Errorf("gemini conversion only supports data URL image inputs; uploaded Gemini file URIs must be sent through the Gemini API format directly")
			}
		}
	}
	return nil
}

func internalToGeminiCodeAssist(req *provider.InternalRequest) ([]byte, error) {
	return internalToGeminiCodeAssistWithAccount(req, nil)
}

func internalToGeminiCodeAssistWithAccount(req *provider.InternalRequest, account *db.Account) ([]byte, error) {
	model := resolveCodeAssistModel(req.Model)
	reqCopy := *req
	reqCopy.Model = model
	gemBody, err := internalToGemini(&reqCopy)
	if err != nil {
		return nil, err
	}
	var vertexReq map[string]interface{}
	if err := provider.DecodeJSONUseNumber(gemBody, &vertexReq); err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"model":          model,
		"user_prompt_id": provider.RandomHex(16),
		"request":        vertexReq,
	}
	if projectID := codeAssistProjectID(account); projectID != "" {
		body["project"] = projectID
	}
	if shouldUseGoogleOneCredits(account, model) {
		body["enabled_credit_types"] = []string{"GOOGLE_ONE_AI"}
	}
	return json.Marshal(body)
}

func resolveCodeAssistModel(model string) string {
	switch model {
	case "", "auto", "auto-gemini-2.5", "pro":
		return "gemini-2.5-pro"
	case "flash":
		return "gemini-2.5-flash"
	case "flash-lite":
		return "gemini-2.5-flash-lite"
	case "auto-gemini-3":
		return "gemini-3-pro-preview"
	default:
		return model
	}
}

func shouldUseGoogleOneCredits(account *db.Account, model string) bool {
	if !isOverageEligibleModel(model) || account == nil || account.Metadata == nil {
		return false
	}
	paidTier, ok := account.Metadata["paid_tier"].(map[string]interface{})
	if !ok {
		return false
	}
	credits, ok := paidTier["availableCredits"].([]interface{})
	if !ok {
		return false
	}
	for _, item := range credits {
		credit, ok := item.(map[string]interface{})
		if !ok || credit["creditType"] != "GOOGLE_ONE_AI" {
			continue
		}
		if amount, ok := credit["creditAmount"].(string); ok {
			parsed, err := strconv.Atoi(amount)
			if err == nil && parsed >= 50 {
				return true
			}
		}
	}
	return false
}

func isOverageEligibleModel(model string) bool {
	switch model {
	case "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3-flash-preview":
		return true
	default:
		return false
	}
}

func codeAssistProjectID(account *db.Account) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	if project, ok := account.Metadata["project_id"].(string); ok {
		return project
	}
	if loadRes, ok := account.Metadata["load_code_assist"].(map[string]interface{}); ok {
		if project, ok := loadRes["cloudaicompanionProject"].(string); ok {
			return project
		}
		if project, ok := loadRes["cloudaicompanionProject"].(map[string]interface{}); ok {
			if id, ok := project["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}

// buildGeminiUserMessage converts an InternalMessage with user role to Gemini format.
func buildGeminiUserMessage(im provider.InternalMessage) (map[string]interface{}, error) {
	parts, err := contentToGeminiParts(im.Content)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"role":  "user",
		"parts": parts,
	}, nil
}

// buildGeminiAssistantMessage converts an InternalMessage with assistant role to Gemini format.
func buildGeminiAssistantMessage(im provider.InternalMessage) ([]interface{}, error) {
	if len(im.ToolCalls) > 0 {
		var parts []interface{}
		// Add text if present
		for _, part := range im.Content {
			if part.Type == "text" && part.Text != "" {
				parts = append(parts, map[string]interface{}{"text": part.Text})
			}
		}
		// Add functionCall parts
		for _, itc := range im.ToolCalls {
			var args json.RawMessage = []byte("{}")
			if itc.Arguments != "" {
				if !json.Valid([]byte(itc.Arguments)) {
					return nil, fmt.Errorf("gemini tool call arguments must be valid JSON")
				}
				args = json.RawMessage(itc.Arguments)
			}
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": itc.Name,
					"args": args,
				},
			})
		}
		return []interface{}{
			map[string]interface{}{
				"role":  "model",
				"parts": parts,
			},
		}, nil
	}

	parts, err := contentToGeminiParts(im.Content)
	if err != nil {
		return nil, err
	}
	return []interface{}{
		map[string]interface{}{
			"role":  "model",
			"parts": parts,
		},
	}, nil
}

// buildGeminiToolResult converts an InternalMessage with tool role to Gemini format.
func buildGeminiToolResult(im provider.InternalMessage) map[string]interface{} {
	name := ""
	content := ""
	toolCallID := ""
	if im.ToolResult != nil {
		name = im.ToolResult.Name
		content = im.ToolResult.Content
		toolCallID = im.ToolResult.ToolCallID
	}
	if name == "" {
		name = toolCallID
	}

	var parts []interface{}
	if content != "" {
		parts = append(parts, map[string]interface{}{
			"text": content,
		})
	}

	return map[string]interface{}{
		"role": "user",
		"parts": []interface{}{
			map[string]interface{}{
				"functionResponse": map[string]interface{}{
					"id":   toolCallID,
					"name": name,
					"response": map[string]interface{}{
						"content": parts,
					},
				},
			},
		},
	}
}

// contentToGeminiParts converts InternalContentPart slice to Gemini parts format.
func contentToGeminiParts(parts []provider.InternalContentPart) ([]interface{}, error) {
	if len(parts) == 0 {
		return []interface{}{map[string]interface{}{"text": ""}}, nil
	}

	var result []interface{}
	for _, part := range parts {
		switch part.Type {
		case "text":
			result = append(result, map[string]interface{}{"text": part.Text})
		case "image_url":
			if part.ImageURL != nil {
				if strings.HasPrefix(*part.ImageURL, "data:") {
					mime, data, ok := parseDataURL(*part.ImageURL)
					if ok {
						result = append(result, map[string]interface{}{
							"inlineData": map[string]interface{}{
								"mimeType": mime,
								"data":     data,
							},
						})
						continue
					}
				}
			}
		default:
			return nil, fmt.Errorf("gemini content part type %q cannot be converted", part.Type)
		}
	}
	if len(result) == 0 {
		return []interface{}{map[string]interface{}{"text": ""}}, nil
	}
	return result, nil
}

func parseDataURL(url string) (mime string, data string, ok bool) {
	rest := strings.TrimPrefix(url, "data:")
	parts := strings.SplitN(rest, ",", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	meta := parts[0]
	data = parts[1]
	mime = strings.TrimSuffix(meta, ";base64")
	if mime == "" || data == "" {
		return "", "", false
	}
	return mime, data, true
}

// buildGeminiToolConfig converts InternalToolChoice to Gemini toolConfig format.
func buildGeminiToolConfig(tc *provider.InternalToolChoice) interface{} {
	switch tc.Type {
	case "auto":
		return map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "AUTO",
			},
		}
	case "none":
		return map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "NONE",
			},
		}
	case "required":
		return map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "ANY",
			},
		}
	case "function":
		return map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode":                 "ANY",
				"allowedFunctionNames": []interface{}{tc.Function},
			},
		}
	default:
		return map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "AUTO",
			},
		}
	}
}
