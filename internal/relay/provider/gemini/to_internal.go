package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// geminiToInternal converts a Gemini API request body
// into the intermediate InternalRequest format.
func geminiToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &req); err != nil {
		return nil, fmt.Errorf("parse gemini request: %w", err)
	}

	ir := &provider.InternalRequest{
		Metadata:    make(map[string]interface{}),
		ExtraParams: make(map[string]interface{}),
	}

	// Model — Gemini doesn't put model in the body, but if it's there, use it
	ir.Model, _ = req["model"].(string)
	if err := validateGeminiRequestFieldsConvertible(req, ir.ExtraParams); err != nil {
		return nil, err
	}

	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}

	// GenerationConfig
	if gcRaw, exists := req["generationConfig"]; exists {
		gc, ok := gcRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini generationConfig must be an object")
		}
		if err := validateGeminiGenerationConfigConvertible(gc, ir.ExtraParams); err != nil {
			return nil, err
		}
		if v, ok := provider.ToFloat64(gc["maxOutputTokens"]); ok && v > 0 {
			tokens := int(v)
			ir.MaxTokens = &tokens
		}
		if v, ok := provider.ToFloat64(gc["temperature"]); ok {
			ir.Temperature = &v
		}
		if v, ok := provider.ToFloat64(gc["topP"]); ok {
			ir.TopP = &v
		}
		if v, ok := provider.ToFloat64(gc["topK"]); ok && v > 0 {
			topK := int(v)
			ir.TopK = &topK
		}
		if v, ok := provider.ToFloat64(gc["candidateCount"]); ok && v > 0 {
			candCount := int(v)
			ir.CandidateCount = &candCount
		}
		if ssRaw, exists := gc["stopSequences"]; exists {
			ss, ok := ssRaw.([]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini generationConfig stopSequences must be an array")
			}
			for _, item := range ss {
				str, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("gemini generationConfig stopSequences entries must be strings")
				}
				ir.StopWords = append(ir.StopWords, str)
			}
		}
	}

	// SystemInstruction → system message
	var systemContent []provider.InternalContentPart
	if siRaw, exists := req["systemInstruction"]; exists {
		si, ok := siRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini systemInstruction must be an object")
		}
		if err := validateAllowedKeys(si, "gemini systemInstruction", "parts"); err != nil {
			return nil, err
		}
		parts, ok := si["parts"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini systemInstruction parts must be an array")
		}
		for _, partRaw := range parts {
			part, ok := partRaw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini systemInstruction part must be an object")
			}
			text, ok := part["text"].(string)
			if !ok || text == "" || len(part) != 1 {
				return nil, fmt.Errorf("gemini systemInstruction part cannot be converted to non-gemini upstream formats")
			}
			systemContent = append(systemContent, provider.InternalContentPart{
				Type: "text",
				Text: text,
			})
		}
	}

	// Contents → messages
	contents, ok := req["contents"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("gemini contents must be an array")
	}
	ir.Messages = make([]provider.InternalMessage, 0, len(contents)+1)

	// Add system message first if present
	if len(systemContent) > 0 {
		ir.Messages = append(ir.Messages, provider.InternalMessage{
			Role:    "system",
			Content: systemContent,
		})
	}

	toolIDsByName := map[string][]string{}
	for _, contentRaw := range contents {
		content, ok := contentRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini contents entries must be objects")
		}
		if err := validateAllowedKeys(content, "gemini content", "role", "parts"); err != nil {
			return nil, err
		}
		im, err := parseGeminiContent(content, toolIDsByName)
		if err != nil {
			return nil, err
		}
		ir.Messages = append(ir.Messages, im)
	}

	// Tools
	if toolsRaw, exists := req["tools"]; exists {
		toolsArr, ok := toolsRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini tools must be an array")
		}
		ir.Tools = make([]provider.InternalTool, 0)
		for _, toolRaw := range toolsArr {
			decls, ok := toolRaw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini tools entries must be objects")
			}
			for key := range decls {
				if key != "functionDeclarations" {
					return nil, fmt.Errorf("gemini tool %q cannot be converted to non-gemini upstream formats", key)
				}
			}
			if fnDecls, ok := decls["functionDeclarations"].([]interface{}); ok {
				for _, fdRaw := range fnDecls {
					fd, ok := fdRaw.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("gemini functionDeclarations entries must be objects")
					}
					if err := validateAllowedKeys(fd, "gemini functionDeclaration", "name", "description", "parameters"); err != nil {
						return nil, err
					}
					it := provider.InternalTool{Type: "function"}
					it.Name, _ = fd["name"].(string)
					if it.Name == "" {
						return nil, fmt.Errorf("gemini functionDeclaration requires name")
					}
					it.Description, _ = fd["description"].(string)
					it.Parameters = fd["parameters"]
					ir.Tools = append(ir.Tools, it)
				}
			} else {
				return nil, fmt.Errorf("gemini tool entry without functionDeclarations cannot be converted to non-gemini upstream formats")
			}
		}
	}

	// ToolConfig → ToolChoice
	if tcRaw, ok := req["toolConfig"]; ok {
		tc, ok := tcRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("gemini toolConfig must be an object")
		}
		choice, err := parseGeminiToolConfig(tc)
		if err != nil {
			return nil, err
		}
		ir.ToolChoice = choice
	}

	// SafetySettings
	if ssRaw, exists := req["safetySettings"]; exists {
		ir.SafetySettings = ssRaw
	}

	// Provider
	if providerVal, exists := req["provider"]; exists {
		ir.Provider, _ = providerVal.(string)
	}

	return ir, nil
}

// parseGeminiContent converts a single Gemini content object to InternalMessage.
func parseGeminiContent(content map[string]interface{}, toolIDsByName map[string][]string) (provider.InternalMessage, error) {
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

	parts, ok := content["parts"].([]interface{})
	if !ok {
		return im, fmt.Errorf("gemini content parts must be an array")
	}
	toolResultSeen := false
	for _, partRaw := range parts {
		part, ok := partRaw.(map[string]interface{})
		if !ok {
			return im, fmt.Errorf("gemini parts entries must be objects")
		}
		if err := validateGeminiPartKeys(part); err != nil {
			return im, err
		}

		// Text part
		handled := false
		if text, ok := part["text"].(string); ok {
			handled = true
			im.Content = append(im.Content, provider.InternalContentPart{
				Type: "text",
				Text: text,
			})
		}
		if inline, ok := part["inlineData"].(map[string]interface{}); ok {
			handled = true
			if err := validateAllowedKeys(inline, "gemini inlineData", "mimeType", "data"); err != nil {
				return im, err
			}
			data, _ := inline["data"].(string)
			mime, _ := inline["mimeType"].(string)
			if data == "" || mime == "" {
				return im, fmt.Errorf("gemini inlineData is missing mimeType or data")
			}
			if !strings.HasPrefix(mime, "image/") {
				return im, fmt.Errorf("gemini inlineData MIME type %q cannot be converted to non-gemini upstream formats", mime)
			}
			url := "data:" + mime + ";base64," + data
			im.Content = append(im.Content, provider.InternalContentPart{Type: "image_url", ImageURL: &url})
		}
		if file, ok := part["fileData"].(map[string]interface{}); ok {
			handled = true
			if uri, _ := file["fileUri"].(string); uri == "" {
				return im, fmt.Errorf("gemini fileData is missing fileUri")
			}
			return im, fmt.Errorf("gemini fileData inputs cannot be converted to non-gemini upstream formats")
		}

		// Function call part
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			handled = true
			if err := validateAllowedKeys(fc, "gemini functionCall", "name", "args"); err != nil {
				return im, err
			}
			name, _ := fc["name"].(string)
			if name == "" {
				return im, fmt.Errorf("gemini functionCall requires name")
			}
			args := "{}"
			if argsVal, exists := fc["args"]; exists {
				a, err := json.Marshal(argsVal)
				if err != nil {
					return im, fmt.Errorf("gemini functionCall args must be JSON-serializable: %w", err)
				}
				args = string(a)
			}
			im.ToolCalls = append(im.ToolCalls, provider.InternalToolCall{
				ID:        newGeminiToolCallID(name, toolIDsByName),
				Name:      name,
				Arguments: args,
			})
		}

		// Function response part (tool result)
		if fr, ok := part["functionResponse"].(map[string]interface{}); ok {
			if toolResultSeen {
				return im, fmt.Errorf("gemini multiple functionResponse parts in one content cannot be converted to the internal format")
			}
			toolResultSeen = true
			handled = true
			if err := validateAllowedKeys(fr, "gemini functionResponse", "id", "name", "response"); err != nil {
				return im, err
			}
			id, _ := fr["id"].(string)
			frName, _ := fr["name"].(string)
			if frName == "" {
				return im, fmt.Errorf("gemini functionResponse requires name")
			}
			if id == "" && frName != "" {
				id = popGeminiToolCallID(frName, toolIDsByName)
			}
			// Extract response content
			var resultContent string
			if resp, ok := fr["response"].(map[string]interface{}); ok {
				if contentParts, ok := resp["content"].([]interface{}); ok {
					var parts []string
					for _, cp := range contentParts {
						cpm, ok := cp.(map[string]interface{})
						if !ok {
							return im, fmt.Errorf("gemini functionResponse response.content entries must be objects")
						}
						if err := validateAllowedKeys(cpm, "gemini functionResponse response.content", "text"); err != nil {
							return im, err
						}
						t, ok := cpm["text"].(string)
						if !ok {
							return im, fmt.Errorf("gemini functionResponse response.content text entry requires text")
						}
						parts = append(parts, t)
					}
					resultContent = joinStrings(parts)
				} else if b, err := json.Marshal(resp); err == nil {
					resultContent = string(b)
				}
			}
			im.Role = "tool"
			im.ToolResult = &provider.InternalToolResult{
				Name:       frName,
				ToolCallID: id,
				Content:    resultContent,
			}
		}
		if !handled {
			return im, fmt.Errorf("gemini part type cannot be converted to non-gemini upstream formats")
		}
	}

	return im, nil
}

func validateGeminiRequestFieldsConvertible(req map[string]interface{}, extraParams map[string]interface{}) error {
	allowed := map[string]struct{}{
		"model":             {},
		"stream":            {},
		"contents":          {},
		"systemInstruction": {},
		"generationConfig":  {},
		"tools":             {},
		"toolConfig":        {},
		"safetySettings":    {},
		"provider":          {},
	}
	for key, val := range req {
		if _, ok := allowed[key]; !ok {
			// Store unknown fields in ExtraParams for lossless round-tripping
			extraParams[key] = val
		}
	}
	return nil
}

func validateGeminiGenerationConfigConvertible(gc map[string]interface{}, extraParams map[string]interface{}) error {
	allowed := map[string]struct{}{
		"maxOutputTokens": {},
		"temperature":     {},
		"topP":            {},
		"topK":            {},
		"stopSequences":   {},
		"candidateCount":  {},
	}
	for key, val := range gc {
		if _, ok := allowed[key]; !ok {
			// Store unknown generationConfig fields in ExtraParams with a
			// prefixed key so they merge back into generationConfig on output.
			extraParams["generationConfig."+key] = val
		}
	}
	return nil
}

func validateGeminiPartKeys(part map[string]interface{}) error {
	allowed := map[string]struct{}{
		"text":             {},
		"inlineData":       {},
		"fileData":         {},
		"functionCall":     {},
		"functionResponse": {},
	}
	known := 0
	for key := range part {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("gemini part field %q cannot be converted to non-gemini upstream formats", key)
		}
		known++
	}
	if known != 1 {
		return fmt.Errorf("gemini part must contain exactly one convertible field")
	}
	return nil
}

func validateAllowedKeys(m map[string]interface{}, label string, keys ...string) error {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for key := range m {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q cannot be converted to non-gemini upstream formats", label, key)
		}
	}
	return nil
}

func newGeminiToolCallID(name string, toolIDsByName map[string][]string) string {
	if name == "" {
		return "call_" + provider.RandomHex(12)
	}
	id := "call_" + provider.RandomHex(12)
	toolIDsByName[name] = append(toolIDsByName[name], id)
	return id
}

func popGeminiToolCallID(name string, toolIDsByName map[string][]string) string {
	if ids := toolIDsByName[name]; len(ids) > 0 {
		id := ids[0]
		toolIDsByName[name] = ids[1:]
		return id
	}
	return "call_" + provider.RandomHex(12)
}

// parseGeminiToolConfig converts Gemini toolConfig to InternalToolChoice.
func parseGeminiToolConfig(tc map[string]interface{}) (*provider.InternalToolChoice, error) {
	if err := validateAllowedKeys(tc, "gemini toolConfig", "functionCallingConfig"); err != nil {
		return nil, err
	}
	fcc, ok := tc["functionCallingConfig"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("gemini toolConfig functionCallingConfig must be an object")
	}
	if err := validateAllowedKeys(fcc, "gemini functionCallingConfig", "mode", "allowedFunctionNames"); err != nil {
		return nil, err
	}
	mode, _ := fcc["mode"].(string)
	switch mode {
	case "AUTO":
		return &provider.InternalToolChoice{Type: "auto"}, nil
	case "NONE":
		return &provider.InternalToolChoice{Type: "none"}, nil
	case "ANY":
		// Check for allowedFunctionNames
		if namesRaw, exists := fcc["allowedFunctionNames"]; exists {
			names, ok := namesRaw.([]interface{})
			if !ok {
				return nil, fmt.Errorf("gemini toolConfig allowedFunctionNames must be an array")
			}
			if len(names) == 0 {
				return &provider.InternalToolChoice{Type: "required"}, nil
			}
			if len(names) > 1 {
				return nil, fmt.Errorf("gemini toolConfig allowedFunctionNames with multiple names cannot be converted to non-gemini upstream formats")
			}
			if name, ok := names[0].(string); ok {
				return &provider.InternalToolChoice{Type: "function", Function: name}, nil
			}
			return nil, fmt.Errorf("gemini toolConfig allowedFunctionNames must contain function names")
		}
		return &provider.InternalToolChoice{Type: "required"}, nil
	default:
		return nil, fmt.Errorf("gemini toolConfig mode %q cannot be converted to non-gemini upstream formats", mode)
	}
}

func joinStrings(parts []string) string {
	return strings.Join(parts, "\n")
}

// RequestToInternal exposes Gemini request parsing for providers with Gemini-shaped requests.
func RequestToInternal(body []byte) (*provider.InternalRequest, error) {
	return geminiToInternal(body)
}
