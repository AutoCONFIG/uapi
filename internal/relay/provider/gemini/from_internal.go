package gemini

import (
	"encoding/json"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
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
				"mode":                "ANY",
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
