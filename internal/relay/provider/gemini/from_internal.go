package gemini

import (
	"encoding/json"
	"strconv"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// internalToGemini converts InternalRequest to Gemini API JSON.
func internalToGemini(req *provider.InternalRequest) ([]byte, error) {
	gemReq := make(map[string]interface{})

	// Convert messages → contents
	var systemInstruction interface{}
	var contents []interface{}

	for _, im := range req.Messages {
		switch im.Role {
		case "system":
			// System → systemInstruction
			parts := contentToGeminiParts(im.Content)
			if len(parts) > 0 {
				systemInstruction = map[string]interface{}{"parts": parts}
			}
		case "tool":
			// Tool result → functionResponse part
			contents = append(contents, buildGeminiToolResult(im))
		case "assistant":
			contents = append(contents, buildGeminiAssistantMessage(im)...)
		default:
			// user
			contents = append(contents, buildGeminiUserMessage(im))
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

	return json.Marshal(gemReq)
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
	if err := json.Unmarshal(gemBody, &vertexReq); err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"model":          model,
		"user_prompt_id": "uapi-" + provider.RandomHex(12),
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
func buildGeminiUserMessage(im provider.InternalMessage) map[string]interface{} {
	parts := contentToGeminiParts(im.Content)
	return map[string]interface{}{
		"role":  "user",
		"parts": parts,
	}
}

// buildGeminiAssistantMessage converts an InternalMessage with assistant role to Gemini format.
func buildGeminiAssistantMessage(im provider.InternalMessage) []interface{} {
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
			args := map[string]interface{}{}
			if itc.Arguments != "" && itc.Arguments != "{}" {
				json.Unmarshal([]byte(itc.Arguments), &args)
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
		}
	}

	parts := contentToGeminiParts(im.Content)
	return []interface{}{
		map[string]interface{}{
			"role":  "model",
			"parts": parts,
		},
	}
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
func contentToGeminiParts(parts []provider.InternalContentPart) []interface{} {
	if len(parts) == 0 {
		return []interface{}{map[string]interface{}{"text": ""}}
	}

	var result []interface{}
	for _, part := range parts {
		switch part.Type {
		case "text":
			result = append(result, map[string]interface{}{"text": part.Text})
		case "image_url":
			if part.ImageURL != nil {
				result = append(result, map[string]interface{}{
					"image_url": map[string]interface{}{
						"url": *part.ImageURL,
					},
				})
			}
		default:
			result = append(result, map[string]interface{}{"text": part.Text})
		}
	}
	if len(result) == 0 {
		return []interface{}{map[string]interface{}{"text": ""}}
	}
	return result
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
