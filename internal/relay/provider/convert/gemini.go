package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// parseGeminiRequest converts Gemini API request to an protocol request view.
func parseGeminiRequest(body []byte) (*requestDraft, error) {
	var req schema.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini request: %w", err)
	}

	ir := &requestDraft{
		Model:        rawString(req.Extra["model"]),
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
		ir.InstructionsRaw = rawJSON(req.SystemInstruction)
		var texts []string
		for _, part := range req.SystemInstruction.Parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
				if len(part.Extra) > 0 {
					appendGeminiPartExtraLosses(ir, "$.systemInstruction.parts[]", part, rawJSON(part))
				}
				continue
			}
			rawPart := rawJSON(part)
			if len(rawPart) > 0 {
				ir.Losses = append(ir.Losses, irloss(FormatGemini, "", "$.systemInstruction.parts[]", "systemInstruction.part", rawPart, "Gemini systemInstruction part has no protocol-neutral instruction representation and is preserved as native raw"))
			}
		}
		if len(texts) > 0 {
			instr := joinNonEmpty(texts, "\n\n")
			ir.Instructions = &instr
		}
	}

	// Convert contents to messages
	for _, content := range req.Contents {
		requestMsg := requestTurnDraft{
			Role: geminiRoleToRequestRole(content.Role),
		}

		for _, part := range content.Parts {
			rawPart := rawJSON(part)
			if len(part.Extra) > 0 {
				appendGeminiPartExtraLosses(ir, "$.contents[].parts[]", part, rawPart)
			}
			switch {
			case part.Text != "" && part.Thought:
				extra := map[string]json.RawMessage{}
				if part.ThoughtSignature != "" {
					extra = setRawString(extra, reasoningExtraThoughtSignature, part.ThoughtSignature)
				}
				appendReasoningItem(&requestMsg, schema.ContentPart{
					Type:  "thinking",
					Text:  part.Text,
					Extra: extra,
				}, rawPart)
			case part.Text != "":
				appendContentItem(&requestMsg, schema.ContentPart{
					Type: "text",
					Text: part.Text,
				}, rawPart)
			case part.InlineData != nil:
				dataURI := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data)
				if strings.HasPrefix(part.InlineData.MimeType, "image/") {
					appendContentItem(&requestMsg, schema.ContentPart{
						Type:     "image_url",
						ImageURL: &dataURI,
						MimeType: part.InlineData.MimeType,
					}, rawPart)
				} else {
					appendContentItem(&requestMsg, schema.ContentPart{
						Type:     "file",
						FileData: dataURI,
						FileType: part.InlineData.MimeType,
						MimeType: part.InlineData.MimeType,
					}, rawPart)
				}
			case part.FunctionCall != nil:
				args := string(part.FunctionCall.Args)
				call := schema.ToolCall{
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
				}
				appendToolCallItem(&requestMsg, call, rawPart)
			case part.FunctionResponse != nil:
				respBytes, _ := json.Marshal(part.FunctionResponse.Response)
				appendToolResultItem(&requestMsg, schema.ToolResult{
					ToolCallID: part.FunctionResponse.Name, // Use function name as ID since Gemini doesn't provide call ID
					Content:    string(respBytes),
				}, rawPart)
				appendGeminiFunctionResponseLosses(ir, part.FunctionResponse, rawPart)
			case part.ThoughtSignature != "":
				appendReasoningItem(&requestMsg, reasoningPartWithExtra("", map[string]json.RawMessage{
					reasoningExtraThoughtSignature: json.RawMessage(fmt.Sprintf(`%q`, part.ThoughtSignature)),
				}), rawPart)
			case part.FileData != nil:
				fileURL := "file://" + part.FileData.FileURI
				if strings.HasPrefix(part.FileData.MimeType, "image/") {
					appendContentItem(&requestMsg, schema.ContentPart{
						Type:     "image_url",
						ImageURL: &fileURL,
						MimeType: part.FileData.MimeType,
					}, rawPart)
				} else {
					appendContentItem(&requestMsg, schema.ContentPart{
						Type:     "file",
						FileURL:  fileURL,
						FileType: part.FileData.MimeType,
						MimeType: part.FileData.MimeType,
					}, rawPart)
				}
			case part.ExecutableCode != nil:
				appendContentItem(&requestMsg, schema.ContentPart{
					Type: "executable_code",
					Text: part.ExecutableCode.Code,
					Extra: map[string]json.RawMessage{
						"language": json.RawMessage(fmt.Sprintf("%q", part.ExecutableCode.Language)),
					},
				}, rawPart)
			case part.CodeExecutionResult != nil:
				appendContentItem(&requestMsg, schema.ContentPart{
					Type: "code_execution_result",
					Text: part.CodeExecutionResult.Output,
					Extra: map[string]json.RawMessage{
						"outcome": json.RawMessage(fmt.Sprintf("%q", part.CodeExecutionResult.Outcome)),
					},
				}, rawPart)
			default:
				if len(rawPart) > 0 {
					appendContentItem(&requestMsg, schema.ContentPart{
						Type:  "gemini_part",
						Extra: part.Extra,
					}, rawPart)
					ir.Losses = append(ir.Losses, irloss(FormatGemini, "", "$.contents[].parts[]", "gemini_part", rawPart, "Gemini part has no protocol-neutral representation and is preserved as native opaque IR"))
				}
			}
		}

		ir.Messages = append(ir.Messages, requestMsg)
	}

	// Generation parameters from GenerationConfig
	if req.GenerationConfig != nil {
		if len(req.GenerationConfig.Extra) > 0 {
			ir.GeminiGenerationConfigExtra = copyRawMap(req.GenerationConfig.Extra)
		}
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
		if req.GenerationConfig.ResponseMimeType != "" {
			ir.ResponseFormat = responseFormatFromGemini(req.GenerationConfig.ResponseMimeType, req.GenerationConfig.ResponseSchema)
		}
	}

	// Safety settings
	if req.SafetySettings != nil {
		ir.SafetySettings = req.SafetySettings
	}

	// Tools
	if req.Tools != nil {
		if tools := parseGeminiRequestTools(req.Tools); len(tools) > 0 {
			ir.Tools = tools
		} else {
			var tools []schema.Tool
			if json.Unmarshal(req.Tools, &tools) == nil {
				ir.Tools = tools
			}
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

func appendGeminiPartExtraLosses(req *requestDraft, path string, part schema.GeminiPart, rawPart json.RawMessage) {
	if req == nil {
		return
	}
	for key, raw := range part.Extra {
		req.Losses = append(req.Losses, irloss(FormatGemini, "", path+"."+key, key, raw, "Gemini part vendor field is preserved in native raw part but not emitted across protocols"))
	}
	if len(part.Extra) > 0 && len(rawPart) > 0 {
		req.Losses = append(req.Losses, irloss(FormatGemini, "", path, "gemini_part", rawPart, "Gemini part native shape is preserved for audit"))
	}
}

func appendGeminiFunctionResponseLosses(req *requestDraft, resp *schema.GeminiFuncResponse, rawPart json.RawMessage) {
	if req == nil || resp == nil {
		return
	}
	add := func(field string, raw json.RawMessage, reason string) {
		if len(raw) == 0 {
			return
		}
		req.Losses = append(req.Losses, irloss(FormatGemini, "", "$.contents[].parts[].functionResponse."+field, field, raw, reason))
	}
	marshalField := func(value interface{}) json.RawMessage {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return raw
	}
	add("response", resp.Response, "Gemini functionResponse.response is serialized into protocol tool-result output text across protocols")
	if resp.ID != "" {
		add("id", marshalField(resp.ID), "Gemini functionResponse.id has no protocol-neutral tool-result field")
	}
	if resp.WillContinue != nil {
		add("willContinue", marshalField(*resp.WillContinue), "Gemini functionResponse.willContinue has no equivalent in target protocols")
	}
	if resp.Scheduling != "" {
		add("scheduling", marshalField(resp.Scheduling), "Gemini functionResponse.scheduling has no equivalent in target protocols")
	}
	add("parts", resp.Parts, "Gemini functionResponse.parts has no equivalent in target protocols")
	for key, raw := range resp.Extra {
		add(key, raw, "Gemini functionResponse vendor field is preserved in native raw part but not emitted across protocols")
	}
	if len(resp.Extra) > 0 && len(rawPart) > 0 {
		req.Losses = append(req.Losses, irloss(FormatGemini, "", "$.contents[].parts[]", "functionResponse", rawPart, "Gemini functionResponse native part is preserved for audit"))
	}
}

// emitGeminiRequest converts an protocol request view to Gemini API request.
func emitGeminiRequest(ir *requestDraft) ([]byte, error) {
	req := make(map[string]interface{})

	// Convert Instructions to systemInstruction
	if ir.Instructions != nil {
		req["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{{"text": *ir.Instructions}},
		}
	}

	// Convert messages to contents
	contents := make([]map[string]interface{}, 0)
	toolCallNames := toolCallNameByID(ir.Messages)
	for _, msg := range ir.Messages {
		contentMap := make(map[string]interface{})
		contentMap["role"] = internalRoleToGemini(msg.Role)

		parts := make([]map[string]interface{}, 0)

		parts = geminiPartsFromItems(ir.SourceFormat, msg, toolCallNames)

		contentMap["parts"] = parts
		contents = append(contents, contentMap)
	}
	req["contents"] = contents

	// Generation config
	genConfig := make(map[string]interface{})
	if ir.MaxTokens != nil {
		genConfig["maxOutputTokens"] = capGeminiMaxOutputTokens(*ir.MaxTokens)
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
	if thinking := geminiThinkingFromProtocolRequest(ir); thinking != nil {
		genConfig["thinkingConfig"] = thinking
	}
	if mimeType, schema := geminiResponseFormat(ir.ResponseFormat); mimeType != "" {
		genConfig["responseMimeType"] = mimeType
		if schema != nil {
			genConfig["responseSchema"] = schema
		}
	}
	for k, v := range ir.GeminiGenerationConfigExtra {
		if _, exists := genConfig[k]; !exists {
			genConfig[k] = v
		}
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

	// Add Extra fields. Gemini CLI envelope fields belong outside the inner
	// request and must not be echoed into request.*.
	for k, v := range ir.Extra {
		if isGeminiEnvelopeFamily(ir.SourceFormat) && isGeminiCLIEnvelopeExtra(k) {
			continue
		}
		req[k] = v
	}

	return json.Marshal(req)
}

func isGeminiEnvelopeFamily(format Format) bool {
	return format == FormatGeminiCLI || format == FormatGeminiCode || format == FormatAntigravity
}

func geminiPartsFromItems(source Format, msg requestTurnDraft, toolCallNames map[string]string) []map[string]interface{} {
	items := canonicalMessageParts(msg)
	parts := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if (source == FormatGemini || isGeminiEnvelopeFamily(source)) && len(item.Raw) > 0 {
			var raw map[string]interface{}
			if err := json.Unmarshal(item.Raw, &raw); err == nil {
				parts = append(parts, raw)
				continue
			}
		}
		if part := geminiPartFromItem(item, toolCallNames); part != nil {
			parts = append(parts, part)
		}
	}
	return parts
}

func geminiPartFromItem(item requestItemDraft, toolCallNames map[string]string) map[string]interface{} {
	switch item.Kind {
	case contentItemKindReasoning:
		return geminiReasoningPart(item.Content)
	case contentItemKindContent:
		return geminiContentPart(item.Content)
	case contentItemKindToolCall:
		return geminiToolCallPart(item.ToolCall)
	case contentItemKindToolResult:
		return geminiToolResultPart(item.ToolResult, toolCallNames)
	default:
		return nil
	}
}

func geminiReasoningPart(rc schema.ContentPart) map[string]interface{} {
	sig := reasoningOpaqueSignature([]schema.ContentPart{rc})
	if rc.Text == "" && sig == "" {
		return nil
	}
	part := map[string]interface{}{}
	if rc.Text != "" {
		part["text"] = rc.Text
		part["thought"] = true
	}
	if sig != "" {
		part["thoughtSignature"] = sig
	}
	return part
}

func geminiContentPart(c schema.ContentPart) map[string]interface{} {
	switch c.Type {
	case "text":
		return map[string]interface{}{"text": c.Text}
	case "image_url":
		if c.ImageURL == nil {
			return nil
		}
		dataURI := *c.ImageURL
		mimeType := "image/png"
		if c.MimeType != "" {
			mimeType = c.MimeType
		}
		data := dataURI
		if strings.HasPrefix(dataURI, "data:") {
			endIdx := len(dataURI)
			for i := len("data:"); i < len(dataURI); i++ {
				if dataURI[i] == ';' || dataURI[i] == ',' {
					endIdx = i
					break
				}
			}
			mimeType = dataURI[len("data:"):endIdx]
			if endIdx < len(dataURI) && dataURI[endIdx] == ';' {
				for i := endIdx + 1; i < len(dataURI); i++ {
					if dataURI[i] == ',' {
						data = dataURI[i+1:]
						break
					}
				}
			}
		} else if strings.HasPrefix(dataURI, "file://") {
			return map[string]interface{}{
				"fileData": map[string]string{
					"fileUri":  strings.TrimPrefix(dataURI, "file://"),
					"mimeType": mimeType,
				},
			}
		}
		return map[string]interface{}{
			"inlineData": map[string]string{
				"mimeType": mimeType,
				"data":     data,
			},
		}
	case "file", "input_file":
		mimeType := c.FileType
		if mimeType == "" {
			mimeType = c.MimeType
		}
		if mimeType == "" {
			mimeType = mimeTypeFromFilename(c.Filename)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if c.FileURL != "" {
			fileURI := strings.TrimPrefix(c.FileURL, "file://")
			return map[string]interface{}{
				"fileData": map[string]string{
					"fileUri":  fileURI,
					"mimeType": mimeType,
				},
			}
		}
		if c.FileData == "" {
			return nil
		}
		data := c.FileData
		if strings.HasPrefix(data, "data:") {
			parsedMime, parsedData, ok := splitDataURI(data)
			if ok {
				mimeType = parsedMime
				data = parsedData
			}
		}
		return map[string]interface{}{
			"inlineData": map[string]string{
				"mimeType": mimeType,
				"data":     data,
			},
		}
	case "executable_code":
		return map[string]interface{}{
			"executableCode": map[string]interface{}{
				"language": rawString(c.Extra["language"]),
				"code":     c.Text,
			},
		}
	case "code_execution_result":
		return map[string]interface{}{
			"codeExecutionResult": map[string]interface{}{
				"outcome": rawString(c.Extra["outcome"]),
				"output":  c.Text,
			},
		}
	default:
		return nil
	}
}

func mimeTypeFromFilename(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".csv"):
		return "text/csv"
	case strings.HasSuffix(lower, ".md"):
		return "text/markdown"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		return ""
	}
}

func geminiToolCallPart(tc schema.ToolCall) map[string]interface{} {
	name := tc.Name
	if name == "" {
		name = tc.Function.Name
	}
	return map[string]interface{}{
		"functionCall": map[string]interface{}{
			"name": name,
			"args": jsonArgumentValue(tc.Function.Arguments),
		},
	}
}

func geminiToolResultPart(result schema.ToolResult, toolCallNames map[string]string) map[string]interface{} {
	var response interface{}
	if len(result.ContentRaw) > 0 {
		_ = json.Unmarshal(result.ContentRaw, &response)
	} else {
		_ = json.Unmarshal([]byte(result.Content), &response)
	}
	return map[string]interface{}{
		"functionResponse": map[string]interface{}{
			"name":     toolResponseName(result.ToolCallID, toolCallNames),
			"response": response,
		},
	}
}

func toolCallNameByID(messages []requestTurnDraft) map[string]string {
	names := make(map[string]string)
	for _, msg := range messages {
		for _, call := range toolCallsFromItems(canonicalMessageParts(msg)) {
			if call.ID == "" || call.Name == "" {
				continue
			}
			names[call.ID] = call.Name
		}
	}
	return names
}

func toolResponseName(toolCallID string, names map[string]string) string {
	if name := names[toolCallID]; name != "" {
		return name
	}
	return toolCallID
}

const geminiMaxOutputTokensCap = 65536

func capGeminiMaxOutputTokens(tokens int) int {
	if tokens > geminiMaxOutputTokensCap {
		return geminiMaxOutputTokensCap
	}
	return tokens
}

func normalizeGeminiThinkingConfig(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return raw
	}
	if len(cfg) == 0 {
		return nil
	}
	normalizeThinkingAlias(cfg, "thinking_budget", "thinkingBudget")
	normalizeThinkingAlias(cfg, "thinking_level", "thinkingLevel")
	normalizeThinkingAlias(cfg, "include_thoughts", "includeThoughts")
	if _, hasBudget := cfg["thinkingBudget"]; hasBudget {
		delete(cfg, "thinkingLevel")
	}
	return cfg
}

func normalizeThinkingAlias(cfg map[string]interface{}, snakeKey, camelKey string) {
	if _, ok := cfg[camelKey]; ok {
		delete(cfg, snakeKey)
		return
	}
	if value, ok := cfg[snakeKey]; ok {
		cfg[camelKey] = value
		delete(cfg, snakeKey)
	}
}

func geminiResponseFormat(raw json.RawMessage) (string, interface{}) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var format map[string]interface{}
	if err := json.Unmarshal(raw, &format); err != nil {
		return "", nil
	}
	formatType, _ := format["type"].(string)
	switch strings.ToLower(strings.TrimSpace(formatType)) {
	case "json_object":
		return "application/json", nil
	case "json_schema":
		if jsonSchema, ok := format["json_schema"].(map[string]interface{}); ok {
			if schema, ok := jsonSchema["schema"]; ok {
				return "application/json", schema
			}
		}
		return "application/json", nil
	default:
		return "", nil
	}
}

func responseFormatFromGemini(mimeType string, schema json.RawMessage) json.RawMessage {
	if !strings.EqualFold(strings.TrimSpace(mimeType), "application/json") {
		return nil
	}
	if len(schema) == 0 || string(schema) == "null" {
		return json.RawMessage(`{"type":"json_object"}`)
	}
	out, err := json.Marshal(map[string]interface{}{
		"type": "json_schema",
		"json_schema": map[string]json.RawMessage{
			"schema": schema,
		},
	})
	if err != nil {
		return json.RawMessage(`{"type":"json_object"}`)
	}
	return out
}

func isGeminiCLIEnvelopeExtra(key string) bool {
	switch key {
	case "project", "user_prompt_id", "enabled_credit_types", "userAgent", "requestType", "requestId", "sessionId", "session_id":
		return true
	default:
		return false
	}
}

func geminiRoleToRequestRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "model", "assistant":
		return "model"
	case "user", "tool":
		return "user"
	default:
		return "unknown"
	}
}

func internalRoleToGemini(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "model":
		return "model"
	case "tool", "function", "user":
		return "user"
	default:
		return "user"
	}
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
	projected := functionToolProjections(tools)
	functionDeclarations := make([]map[string]interface{}, 0, len(projected))
	for _, tool := range projected {
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
		parameters := tool.Function.Parameters
		if len(parameters) == 0 {
			parameters = tool.Parameters
		}
		if len(parameters) == 0 {
			parameters = tool.InputSchema
		}
		description := tool.Function.Description
		if description == "" {
			description = tool.Description
		}
		name := tool.Function.Name
		if name == "" {
			name = tool.Name
		}
		return strings.TrimSpace(name), strings.TrimSpace(description), parameters
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

func parseGeminiRequestTools(raw json.RawMessage) []schema.Tool {
	var rawTools []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawTools); err != nil {
		return nil
	}
	tools := make([]schema.Tool, 0)
	for _, rawTool := range rawTools {
		rawDecls := rawTool["functionDeclarations"]
		if len(rawDecls) == 0 {
			rawDecls = rawTool["function_declarations"]
		}
		if len(rawDecls) == 0 || string(rawDecls) == "null" {
			continue
		}
		var declarations []map[string]json.RawMessage
		if err := json.Unmarshal(rawDecls, &declarations); err != nil {
			continue
		}
		for _, declaration := range declarations {
			var name string
			if err := json.Unmarshal(declaration["name"], &name); err != nil || strings.TrimSpace(name) == "" {
				continue
			}
			var description string
			_ = json.Unmarshal(declaration["description"], &description)
			parameters := declaration["parametersJsonSchema"]
			if len(parameters) == 0 {
				parameters = declaration["parameters"]
			}
			if string(parameters) == "null" {
				parameters = nil
			}
			tools = append(tools, schema.Tool{
				Type:        "function",
				Name:        strings.TrimSpace(name),
				Description: strings.TrimSpace(description),
				Parameters:  parameters,
			})
		}
	}
	return tools
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
	namespace := toolChoiceNamespace(obj)
	if name := firstString(obj, "name", "Name"); name != "" {
		return qualifyResponsesNamespaceToolName(namespace, name)
	}
	for _, key := range []string{"function", "Function"} {
		switch raw := obj[key].(type) {
		case string:
			return qualifyResponsesNamespaceToolName(namespace, raw)
		case map[string]interface{}:
			if name := firstString(raw, "name", "Name"); name != "" {
				return qualifyResponsesNamespaceToolName(namespace, name)
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
	registerRequestIRParser(FormatGemini, parseGeminiRequestIR)
	registerRequestIREmitter(FormatGemini, emitGeminiRequestIR)
}
