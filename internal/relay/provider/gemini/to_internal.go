package gemini

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

// geminiToInternal converts a Gemini API request body
// into the intermediate InternalRequest format.
func geminiToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse gemini request: %w", err)
	}

	ir := &provider.InternalRequest{
		Metadata: make(map[string]interface{}),
	}

	// Model — Gemini doesn't put model in the body, but if it's there, use it
	ir.Model, _ = req["model"].(string)

	// GenerationConfig
	if gc, ok := req["generationConfig"].(map[string]interface{}); ok {
		if v, ok := gc["maxOutputTokens"].(float64); ok && v > 0 {
			tokens := int(v)
			ir.MaxTokens = &tokens
		}
		if v, ok := gc["temperature"].(float64); ok {
			ir.Temperature = &v
		}
		if v, ok := gc["topP"].(float64); ok {
			ir.TopP = &v
		}
		if ss, ok := gc["stopSequences"].([]interface{}); ok {
			for _, item := range ss {
				if str, ok := item.(string); ok {
					ir.StopWords = append(ir.StopWords, str)
				}
			}
		}
	}

	// SystemInstruction → system message
	var systemContent []provider.InternalContentPart
	if si, ok := req["systemInstruction"].(map[string]interface{}); ok {
		if parts, ok := si["parts"].([]interface{}); ok {
			for _, partRaw := range parts {
				if part, ok := partRaw.(map[string]interface{}); ok {
					if text, ok := part["text"].(string); ok {
						systemContent = append(systemContent, provider.InternalContentPart{
							Type: "text",
							Text: text,
						})
					}
				}
			}
		}
	}

	// Contents → messages
	contents, _ := req["contents"].([]interface{})
	ir.Messages = make([]provider.InternalMessage, 0, len(contents)+1)

	// Add system message first if present
	if len(systemContent) > 0 {
		ir.Messages = append(ir.Messages, provider.InternalMessage{
			Role:    "system",
			Content: systemContent,
		})
	}

	for _, contentRaw := range contents {
		content, ok := contentRaw.(map[string]interface{})
		if !ok {
			continue
		}
		im := parseGeminiContent(content)
		ir.Messages = append(ir.Messages, im)
	}

	// Tools
	if toolsArr, ok := req["tools"].([]interface{}); ok && len(toolsArr) > 0 {
		// Gemini format: [{functionDeclarations: [...]}]
		if decls, ok := toolsArr[0].(map[string]interface{}); ok {
			if fnDecls, ok := decls["functionDeclarations"].([]interface{}); ok {
				ir.Tools = make([]provider.InternalTool, 0, len(fnDecls))
				for _, fdRaw := range fnDecls {
					fd, ok := fdRaw.(map[string]interface{})
					if !ok {
						continue
					}
					it := provider.InternalTool{Type: "function"}
					it.Name, _ = fd["name"].(string)
					it.Description, _ = fd["description"].(string)
					it.Parameters = fd["parameters"]
					ir.Tools = append(ir.Tools, it)
				}
			}
		}
	}

	// ToolConfig → ToolChoice
	if tc, ok := req["toolConfig"].(map[string]interface{}); ok {
		ir.ToolChoice = parseGeminiToolConfig(tc)
	}

	return ir, nil
}

// parseGeminiContent converts a single Gemini content object to InternalMessage.
func parseGeminiContent(content map[string]interface{}) provider.InternalMessage {
	im := provider.InternalMessage{}
	role, _ := content["role"].(string)

	// Map Gemini roles to internal roles
	switch role {
	case "user":
		im.Role = "user"
	case "model":
		im.Role = "assistant"
	default:
		im.Role = role
	}

	parts, _ := content["parts"].([]interface{})
	for _, partRaw := range parts {
		part, ok := partRaw.(map[string]interface{})
		if !ok {
			continue
		}

		// Text part
		if text, ok := part["text"].(string); ok {
			im.Content = append(im.Content, provider.InternalContentPart{
				Type: "text",
				Text: text,
			})
		}

		// Function call part
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			args := "{}"
			if a, err := json.Marshal(fc["args"]); err == nil {
				args = string(a)
			}
			im.ToolCalls = append(im.ToolCalls, provider.InternalToolCall{
				ID:        "call_" + randomHex(12),
				Name:      name,
				Arguments: args,
			})
		}

		// Function response part (tool result)
		if fr, ok := part["functionResponse"].(map[string]interface{}); ok {
			id, _ := fr["id"].(string)
			// Extract response content
			var resultContent string
			if resp, ok := fr["response"].(map[string]interface{}); ok {
				if contentParts, ok := resp["content"].([]interface{}); ok {
					var parts []string
					for _, cp := range contentParts {
						if cpm, ok := cp.(map[string]interface{}); ok {
							if t, ok := cpm["text"].(string); ok {
								parts = append(parts, t)
							}
						}
					}
					resultContent = joinStrings(parts)
				}
			}
			im.Role = "tool"
			im.ToolResult = &provider.InternalToolResult{
				ToolCallID: id,
				Content:    resultContent,
			}
		}
	}

	return im
}

// parseGeminiToolConfig converts Gemini toolConfig to InternalToolChoice.
func parseGeminiToolConfig(tc map[string]interface{}) *provider.InternalToolChoice {
	fcc, ok := tc["functionCallingConfig"].(map[string]interface{})
	if !ok {
		return &provider.InternalToolChoice{Type: "auto"}
	}
	mode, _ := fcc["mode"].(string)
	switch mode {
	case "AUTO":
		return &provider.InternalToolChoice{Type: "auto"}
	case "NONE":
		return &provider.InternalToolChoice{Type: "none"}
	case "ANY":
		// Check for allowedFunctionNames
		if names, ok := fcc["allowedFunctionNames"].([]interface{}); ok && len(names) > 0 {
			if name, ok := names[0].(string); ok {
				return &provider.InternalToolChoice{Type: "function", Function: name}
			}
		}
		return &provider.InternalToolChoice{Type: "required"}
	default:
		return &provider.InternalToolChoice{Type: "auto"}
	}
}

func joinStrings(parts []string) string {
	result := ""
	for i, s := range parts {
		if i > 0 {
			result += "\n"
		}
		result += s
	}
	return result
}
