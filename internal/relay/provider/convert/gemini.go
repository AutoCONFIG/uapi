package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// GeminiToInternal converts Gemini API request to InternalRequest.
func GeminiToInternal(body []byte) (*InternalRequest, error) {
	var req schema.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini request: %w", err)
	}

	ir := &InternalRequest{
		Model:        "", // Will be set by caller
		Stream:       false,
		SourceFormat: FormatGemini,
		Extra:        make(map[string]json.RawMessage),
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
	}

	// Convert systemInstruction to Instructions
	if req.SystemInstruction != nil && len(req.SystemInstruction.Parts) > 0 {
		var texts []string
		for _, part := range req.SystemInstruction.Parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) > 0 {
			instr := joinNonEmpty(texts, "\n\n")
			ir.Instructions = &instr
		}
	}

	// Convert contents to messages
	for _, content := range req.Contents {
		internalMsg := InternalMessage{
			Role: content.Role,
		}

		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				internalMsg.Content = append(internalMsg.Content, schema.ContentPart{
					Type: "text",
					Text: part.Text,
				})
			case part.InlineData != nil:
				dataURI := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data)
				internalMsg.Content = append(internalMsg.Content, schema.ContentPart{
					Type:     "image_url",
					ImageURL: &dataURI,
				})
			case part.FunctionCall != nil:
				args := string(part.FunctionCall.Args)
				internalMsg.ToolCalls = append(internalMsg.ToolCalls, schema.ToolCall{
					ID:   "", // Gemini doesn't provide ID for function calls
					Type: "function",
					Name: part.FunctionCall.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      part.FunctionCall.Name,
						Arguments: args,
					},
				})
			case part.FunctionResponse != nil:
				respBytes, _ := json.Marshal(part.FunctionResponse.Response)
				internalMsg.ToolResult = &schema.ToolResult{
					ToolCallID: part.FunctionResponse.Name, // Use function name as ID since Gemini doesn't provide call ID
					Content:    string(respBytes),
				}
			case part.FileData != nil:
				// Convert file data to content
				internalMsg.Content = append(internalMsg.Content, schema.ContentPart{
					Type: "image_url",
					Text: fmt.Sprintf("file://%s", part.FileData.FileURI), // Convert to URL format
				})
			}
		}

		ir.Messages = append(ir.Messages, internalMsg)
	}

	// Generation parameters from GenerationConfig
	if req.GenerationConfig != nil {
		if req.GenerationConfig.MaxOutputTokens != nil {
			ir.MaxTokens = req.GenerationConfig.MaxOutputTokens
		}
		if req.GenerationConfig.Temperature != nil {
			ir.Temperature = req.GenerationConfig.Temperature
		}
		if req.GenerationConfig.TopP != nil {
			ir.TopP = req.GenerationConfig.TopP
		}
		if req.GenerationConfig.TopK != nil {
			ir.TopK = req.GenerationConfig.TopK
		}
		if len(req.GenerationConfig.StopSequences) > 0 {
			ir.StopWords = req.GenerationConfig.StopSequences
		}
		if req.GenerationConfig.CandidateCount != nil {
			ir.CandidateCount = req.GenerationConfig.CandidateCount
		}
		if req.GenerationConfig.ThinkingConfig != nil {
			ir.Thinking = req.GenerationConfig.ThinkingConfig
		}
	}

	// Safety settings
	if req.SafetySettings != nil {
		ir.SafetySettings = req.SafetySettings
	}

	// Tools
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			ir.Tools = tools
		}
	}

	// Tool config
	if req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig != nil {
		fcConfig := req.ToolConfig.FunctionCallingConfig
		mode := normalizeGeminiFunctionCallingMode(fcConfig.Mode)
		if mode != "" {
			toolChoice := json.RawMessage(fmt.Sprintf(`{"mode":%q}`, mode))
			if len(fcConfig.AllowedFunctionNames) > 0 {
				names, _ := json.Marshal(fcConfig.AllowedFunctionNames)
				toolChoice = json.RawMessage(fmt.Sprintf(`{"mode":%q,"function_names":%s}`, mode, names))
			}
			ir.ToolChoice = toolChoice
		}
	}

	return ir, nil
}

// InternalToGemini converts InternalRequest to Gemini API request.
func InternalToGemini(ir *InternalRequest) ([]byte, error) {
	req := make(map[string]interface{})

	// Convert Instructions to systemInstruction
	if ir.Instructions != nil {
		req["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{{"text": *ir.Instructions}},
		}
	}

	// Convert messages to contents
	contents := make([]map[string]interface{}, 0)
	for _, msg := range ir.Messages {
		contentMap := make(map[string]interface{})
		contentMap["role"] = msg.Role

		parts := make([]map[string]interface{}, 0)

		// Convert content to parts
		for _, c := range msg.Content {
			switch c.Type {
			case "text":
				parts = append(parts, map[string]interface{}{"text": c.Text})
			case "image_url":
				if c.ImageURL != nil {
					// Parse data URI to extract mime type and data
					dataURI := *c.ImageURL
					mimeType := "image/png" // default
					data := dataURI

					if len(dataURI) > 5 && dataURI[:5] == "data:" {
						// Extract mime type
						endIdx := len(dataURI)
						for i := 5; i < len(dataURI); i++ {
							if dataURI[i] == ';' || dataURI[i] == ',' {
								endIdx = i
								break
							}
						}
						mimeType = dataURI[5:endIdx]

						// Extract base64 data
						if endIdx < len(dataURI) && dataURI[endIdx] == ';' {
							for i := endIdx + 1; i < len(dataURI); i++ {
								if dataURI[i] == ',' {
									data = dataURI[i+1:]
									break
								}
							}
						}
					} else if len(dataURI) > 7 && dataURI[:7] == "file://" {
						// Handle file:// URLs
						parts = append(parts, map[string]interface{}{
							"fileData": map[string]string{
								"fileUri":  dataURI[7:],
								"mimeType": mimeType,
							},
						})
						continue
					}

					parts = append(parts, map[string]interface{}{
						"inlineData": map[string]string{
							"mimeType": mimeType,
							"data":     data,
						},
					})
				}
			}
		}

		// Convert tool calls to functionCall parts
		for _, tc := range msg.ToolCalls {
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": tc.Name,
					"args": tc.Function.Arguments,
				},
			})
		}

		// Convert tool result to functionResponse part
		if msg.ToolResult != nil {
			var response interface{}
			json.Unmarshal([]byte(msg.ToolResult.Content), &response)
			parts = append(parts, map[string]interface{}{
				"functionResponse": map[string]interface{}{
					"name":     msg.ToolResult.ToolCallID,
					"response": response,
				},
			})
		}

		contentMap["parts"] = parts
		contents = append(contents, contentMap)
	}
	req["contents"] = contents

	// Generation config
	genConfig := make(map[string]interface{})
	if ir.MaxTokens != nil {
		genConfig["maxOutputTokens"] = *ir.MaxTokens
	}
	if ir.Temperature != nil {
		genConfig["temperature"] = *ir.Temperature
	}
	if ir.TopP != nil {
		genConfig["topP"] = *ir.TopP
	}
	if ir.TopK != nil {
		genConfig["topK"] = *ir.TopK
	}
	if len(ir.StopWords) > 0 {
		genConfig["stopSequences"] = ir.StopWords
	}
	if ir.CandidateCount != nil {
		genConfig["candidateCount"] = *ir.CandidateCount
	}
	if ir.Thinking != nil {
		genConfig["thinkingConfig"] = ir.Thinking
	}
	if len(genConfig) > 0 {
		req["generationConfig"] = genConfig
	}

	// Safety settings
	if ir.SafetySettings != nil {
		req["safetySettings"] = ir.SafetySettings
	}

	hasGeminiTools := false
	if ir.Tools != nil {
		if tools := geminiTools(ir.Tools); len(tools) > 0 {
			req["tools"] = tools
			hasGeminiTools = true
		}
	}

	// Tool config
	if hasGeminiTools && ir.ToolChoice != nil {
		if fcConfig, ok := geminiFunctionCallingConfig(ir.ToolChoice); ok {
			toolConfig := map[string]interface{}{
				"functionCallingConfig": fcConfig,
			}
			req["toolConfig"] = toolConfig
		}
	}

	// Add Extra fields
	for k, v := range ir.Extra {
		req[k] = v
	}

	return json.Marshal(req)
}

func geminiFunctionCallingConfig(raw json.RawMessage) (map[string]interface{}, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}

	var choice string
	if err := json.Unmarshal(raw, &choice); err == nil {
		mode := openAIToolChoiceTypeToGeminiMode(choice)
		if mode == "" {
			mode = normalizeGeminiFunctionCallingMode(choice)
		}
		if mode == "" {
			return nil, false
		}
		return map[string]interface{}{"mode": mode}, true
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	mode := firstString(obj, "mode", "Mode")
	choiceType := firstString(obj, "type", "Type")
	if mode == "" {
		mode = openAIToolChoiceTypeToGeminiMode(choiceType)
	}
	mode = normalizeGeminiFunctionCallingMode(mode)
	if mode == "" {
		return nil, false
	}

	allowed := firstStringSlice(obj, "allowedFunctionNames", "function_names", "AllowedFunctionNames")
	if name := functionChoiceName(obj); name != "" {
		allowed = appendIfMissing(allowed, name)
		if mode == "AUTO" && strings.EqualFold(choiceType, "function") {
			mode = "ANY"
		}
	}

	config := map[string]interface{}{"mode": mode}
	if len(allowed) > 0 {
		config["allowedFunctionNames"] = allowed
	}
	return config, true
}

func geminiTools(tools []schema.Tool) []map[string]interface{} {
	functionDeclarations := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		name, description, parameters := normalizedFunctionTool(tool)
		if name == "" {
			continue
		}
		declaration := map[string]interface{}{
			"name": name,
		}
		if description != "" {
			declaration["description"] = description
		}
		if len(parameters) > 0 && string(parameters) != "null" {
			declaration["parametersJsonSchema"] = json.RawMessage(parameters)
		}
		functionDeclarations = append(functionDeclarations, declaration)
	}
	if len(functionDeclarations) == 0 {
		return nil
	}
	return []map[string]interface{}{
		{"functionDeclarations": functionDeclarations},
	}
}

func normalizedFunctionTool(tool schema.Tool) (string, string, json.RawMessage) {
	if tool.Function != nil {
		return strings.TrimSpace(tool.Function.Name), strings.TrimSpace(tool.Function.Description), tool.Function.Parameters
	}
	if tool.Type != "" && tool.Type != "function" {
		return "", "", nil
	}
	parameters := tool.Parameters
	if len(parameters) == 0 {
		parameters = tool.InputSchema
	}
	return strings.TrimSpace(tool.Name), strings.TrimSpace(tool.Description), parameters
}

func normalizeGeminiFunctionCallingMode(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "AUTO", "NONE", "ANY", "VALIDATED":
		return strings.ToUpper(strings.TrimSpace(mode))
	case "REQUIRED":
		return "ANY"
	default:
		return ""
	}
}

func openAIToolChoiceTypeToGeminiMode(choice string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "auto":
		return "AUTO"
	case "none":
		return "NONE"
	case "required", "function":
		return "ANY"
	default:
		return ""
	}
}

func firstString(obj map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := obj[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func firstStringSlice(obj map[string]interface{}, keys ...string) []string {
	for _, key := range keys {
		switch raw := obj[key].(type) {
		case []string:
			return raw
		case []interface{}:
			out := make([]string, 0, len(raw))
			for _, item := range raw {
				if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
					out = append(out, strings.TrimSpace(value))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func functionChoiceName(obj map[string]interface{}) string {
	for _, key := range []string{"function", "Function"} {
		switch raw := obj[key].(type) {
		case string:
			return strings.TrimSpace(raw)
		case map[string]interface{}:
			if name := firstString(raw, "name", "Name"); name != "" {
				return name
			}
		}
	}
	return ""
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func init() {
	RegisterToInternal(FormatGemini, GeminiToInternal)
	RegisterFromInternal(FormatGemini, InternalToGemini)
}
