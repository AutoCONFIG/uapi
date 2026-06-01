package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseGeminiRequestDirectIR(body []byte) (*relayir.Request, error) {
	var req schema.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini request: %w", err)
	}
	out := &relayir.Request{
		SourceProtocol: relayir.ProtocolGemini,
		Model:          rawString(req.Extra["model"]),
		Metadata:       relayir.CloneRawMap(req.Extra),
		Native:         relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, RawBody: relayir.CloneRaw(body), Fields: relayir.CloneRawMap(req.Extra), Unknown: relayir.CloneRawMap(req.Extra)},
	}
	if req.SystemInstruction != nil {
		out.Instructions = append(out.Instructions, geminiInstructionToIR(*req.SystemInstruction, out))
	}
	for _, content := range req.Contents {
		out.Turns = append(out.Turns, geminiContentToIRTurn(content, out))
	}
	if req.GenerationConfig != nil {
		out.Generation.Extra = relayir.CloneRawMap(req.GenerationConfig.Extra)
		out.Generation.MaxTokens = req.GenerationConfig.MaxOutputTokens
		out.Generation.Temperature = req.GenerationConfig.Temperature
		out.Generation.TopP = req.GenerationConfig.TopP
		out.Generation.TopK = req.GenerationConfig.TopK
		out.Generation.Stop = append([]string(nil), req.GenerationConfig.StopSequences...)
		out.Generation.CandidateCount = req.GenerationConfig.CandidateCount
		out.Generation.Thinking = relayir.CloneRaw(req.GenerationConfig.ThinkingConfig)
		if req.GenerationConfig.ResponseMimeType != "" {
			out.Generation.ResponseFormat = responseFormatFromGemini(req.GenerationConfig.ResponseMimeType, req.GenerationConfig.ResponseSchema)
		}
	}
	out.Safety.Settings = relayir.CloneRaw(req.SafetySettings)
	if req.Tools != nil {
		if tools := parseGeminiRequestTools(req.Tools); len(tools) > 0 {
			for _, tool := range tools {
				out.Tools = append(out.Tools, irTool(tool, FormatGemini))
			}
		} else {
			var tools []schema.Tool
			if json.Unmarshal(req.Tools, &tools) == nil {
				for _, tool := range tools {
					out.Tools = append(out.Tools, irTool(tool, FormatGemini))
				}
			}
		}
	}
	if req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig != nil {
		fcConfig := req.ToolConfig.FunctionCallingConfig
		mode := normalizeGeminiFunctionCallingMode(fcConfig.Mode)
		if mode != "" {
			toolChoice := json.RawMessage(fmt.Sprintf(`{"mode":%q}`, mode))
			if len(fcConfig.AllowedFunctionNames) > 0 {
				names, _ := json.Marshal(fcConfig.AllowedFunctionNames)
				toolChoice = json.RawMessage(fmt.Sprintf(`{"mode":%q,"function_names":%s}`, mode, names))
			}
			out.ToolChoice = &relayir.ToolChoice{Raw: toolChoice}
		}
	}
	return out, nil
}

func geminiInstructionToIR(content schema.GeminiContent, req *relayir.Request) relayir.Instruction {
	inst := relayir.Instruction{Role: relayir.RoleSystem, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "systemInstruction", Raw: rawJSON(content)}}
	var texts []string
	for idx, part := range content.Parts {
		item := geminiPartToIRItem(part, idx, req, "$.systemInstruction.parts[]")
		inst.Items = append(inst.Items, item)
		if item.Text != nil && item.Text.Text != "" {
			texts = append(texts, item.Text.Text)
		} else if req != nil {
			rawPart := rawJSON(part)
			req.Losses = append(req.Losses, irloss(FormatGemini, "", "$.systemInstruction.parts[]", "systemInstruction.part", rawPart, "Gemini systemInstruction part has no protocol-neutral instruction representation and is preserved as native raw"))
		}
	}
	inst.Text = joinNonEmpty(texts, "\n\n")
	return inst
}

func geminiContentToIRTurn(content schema.GeminiContent, req *relayir.Request) relayir.Turn {
	turn := relayir.Turn{Role: relayir.Role(geminiRoleToRequestRole(content.Role)), Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "content", Raw: rawJSON(content)}}
	for idx, part := range content.Parts {
		turn.Items = append(turn.Items, geminiPartToIRItem(part, idx, req, "$.contents[].parts[]"))
	}
	return turn
}

func geminiPartToIRItem(part schema.GeminiPart, idx int, req *relayir.Request, path string) relayir.Item {
	rawPart := rawJSON(part)
	if len(part.Extra) > 0 && req != nil {
		for key, raw := range part.Extra {
			req.Losses = append(req.Losses, irloss(FormatGemini, "", path+"."+key, key, raw, "Gemini part vendor field is preserved in native raw part but not emitted across protocols"))
		}
		req.Losses = append(req.Losses, irloss(FormatGemini, "", path, "gemini_part", rawPart, "Gemini part native shape is preserved for audit"))
	}
	switch {
	case part.Text != "" && part.Thought:
		extra := map[string]json.RawMessage{}
		if part.ThoughtSignature != "" {
			extra = setRawString(extra, reasoningExtraThoughtSignature, part.ThoughtSignature)
		}
		return irContentPartItem(contentItemKindReasoning, schema.ContentPart{Type: "thinking", Text: part.Text, Extra: extra}, rawPart, FormatGemini, idx)
	case part.Text != "":
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "text", Text: part.Text}, rawPart, FormatGemini, idx)
	case part.InlineData != nil:
		dataURI := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data)
		if strings.HasPrefix(part.InlineData.MimeType, "image/") {
			return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "image_url", ImageURL: &dataURI, MimeType: part.InlineData.MimeType}, rawPart, FormatGemini, idx)
		}
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "file", FileData: dataURI, FileType: part.InlineData.MimeType, MimeType: part.InlineData.MimeType}, rawPart, FormatGemini, idx)
	case part.FunctionCall != nil:
		return irToolUseItem(schema.ToolCall{Type: "function", Name: part.FunctionCall.Name, Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: part.FunctionCall.Name, Arguments: string(part.FunctionCall.Args)}}, rawPart, FormatGemini, idx)
	case part.FunctionResponse != nil:
		respBytes, _ := json.Marshal(part.FunctionResponse.Response)
		if req != nil {
			appendGeminiFunctionResponseLossesIR(req, part.FunctionResponse, rawPart)
		}
		return irToolResultItem(schema.ToolResult{ToolCallID: part.FunctionResponse.Name, Content: string(respBytes), ContentRaw: relayir.CloneRaw(part.FunctionResponse.Response)}, rawPart, FormatGemini, idx)
	case part.ThoughtSignature != "":
		return irContentPartItem(contentItemKindReasoning, reasoningPartWithExtra("", map[string]json.RawMessage{reasoningExtraThoughtSignature: json.RawMessage(fmt.Sprintf(`%q`, part.ThoughtSignature))}), rawPart, FormatGemini, idx)
	case part.FileData != nil:
		fileURL := "file://" + part.FileData.FileURI
		if strings.HasPrefix(part.FileData.MimeType, "image/") {
			return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "image_url", ImageURL: &fileURL, MimeType: part.FileData.MimeType}, rawPart, FormatGemini, idx)
		}
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "file", FileURL: fileURL, FileType: part.FileData.MimeType, MimeType: part.FileData.MimeType}, rawPart, FormatGemini, idx)
	case part.ExecutableCode != nil:
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "executable_code", Text: part.ExecutableCode.Code, Extra: setRawString(nil, "language", part.ExecutableCode.Language)}, rawPart, FormatGemini, idx)
	case part.CodeExecutionResult != nil:
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "code_execution_result", Text: part.CodeExecutionResult.Output, Extra: setRawString(nil, "outcome", part.CodeExecutionResult.Outcome)}, rawPart, FormatGemini, idx)
	default:
		if req != nil && len(rawPart) > 0 {
			req.Losses = append(req.Losses, irloss(FormatGemini, "", path, "gemini_part", rawPart, "Gemini part has no protocol-neutral representation and is preserved as native opaque IR"))
		}
		return relayir.Item{Kind: relayir.ItemOpaque, OriginalIndex: idx, Opaque: &relayir.Opaque{Type: "gemini_part", Raw: rawPart}, Metadata: relayir.CloneRawMap(part.Extra), Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "gemini_part", Raw: rawPart, Fields: relayir.CloneRawMap(part.Extra), Index: idx}}
	}
}

func appendGeminiFunctionResponseLossesIR(req *relayir.Request, resp *schema.GeminiFuncResponse, rawPart json.RawMessage) {
	if req == nil || resp == nil {
		return
	}
	req.Losses = append(req.Losses, geminiFunctionResponseLosses(resp)...)
	if len(resp.Extra) > 0 && len(rawPart) > 0 {
		req.Losses = append(req.Losses, irloss(FormatGemini, "", "$.contents[].parts[]", "functionResponse", rawPart, "Gemini functionResponse native part is preserved for audit"))
	}
}

func geminiFunctionResponseLosses(resp *schema.GeminiFuncResponse) []relayir.Loss {
	if resp == nil {
		return nil
	}
	var losses []relayir.Loss
	add := func(field string, raw json.RawMessage, reason string) {
		if len(raw) > 0 {
			losses = append(losses, irloss(FormatGemini, "", "$.contents[].parts[].functionResponse."+field, field, raw, reason))
		}
	}
	add("response", resp.Response, "Gemini functionResponse.response is serialized into protocol tool-result output text across protocols")
	if resp.ID != "" {
		add("id", rawJSON(resp.ID), "Gemini functionResponse.id has no protocol-neutral tool-result field")
	}
	if resp.WillContinue != nil {
		add("willContinue", rawJSON(*resp.WillContinue), "Gemini functionResponse.willContinue has no equivalent in target protocols")
	}
	if resp.Scheduling != "" {
		add("scheduling", rawJSON(resp.Scheduling), "Gemini functionResponse.scheduling has no equivalent in target protocols")
	}
	add("parts", resp.Parts, "Gemini functionResponse.parts has no equivalent in target protocols")
	for key, raw := range resp.Extra {
		add(key, raw, "Gemini functionResponse vendor field is preserved in native raw part but not emitted across protocols")
	}
	return losses
}

func emitGeminiRequestDirectIR(reqIR *relayir.Request) ([]byte, error) {
	out := make(map[string]interface{})
	if len(reqIR.Instructions) > 0 {
		var parts []map[string]interface{}
		for _, inst := range reqIR.Instructions {
			for _, item := range inst.Items {
				part, err := geminiPartFromIRItem(item, nil)
				if err != nil {
					return nil, err
				}
				if part != nil {
					parts = append(parts, part)
				}
			}
			if len(inst.Items) == 0 && inst.Text != "" {
				parts = append(parts, map[string]interface{}{"text": inst.Text})
			}
		}
		if len(parts) > 0 {
			out["systemInstruction"] = map[string]interface{}{"parts": parts}
		}
	}
	names := toolCallNameByIDIR(reqIR.Turns)
	var contents []map[string]interface{}
	for _, turn := range reqIR.Turns {
		parts, err := geminiPartsFromIRTurn(reqIR.SourceProtocol, turn, names)
		if err != nil {
			return nil, err
		}
		contents = append(contents, map[string]interface{}{
			"role":  internalRoleToGemini(string(turn.Role)),
			"parts": parts,
		})
	}
	out["contents"] = contents
	genConfig := map[string]interface{}{}
	if reqIR.Generation.MaxTokens != nil {
		genConfig["maxOutputTokens"] = capGeminiMaxOutputTokens(*reqIR.Generation.MaxTokens)
	}
	if reqIR.Generation.Temperature != nil {
		genConfig["temperature"] = *reqIR.Generation.Temperature
	}
	if reqIR.Generation.TopP != nil {
		genConfig["topP"] = *reqIR.Generation.TopP
	}
	if reqIR.Generation.TopK != nil {
		genConfig["topK"] = *reqIR.Generation.TopK
	}
	if len(reqIR.Generation.Stop) > 0 {
		genConfig["stopSequences"] = reqIR.Generation.Stop
	}
	if reqIR.Generation.CandidateCount != nil {
		genConfig["candidateCount"] = *reqIR.Generation.CandidateCount
	}
	if thinking := geminiThinkingFromIRRequest(reqIR); thinking != nil {
		genConfig["thinkingConfig"] = thinking
	}
	if mimeType, schema := geminiResponseFormat(reqIR.Generation.ResponseFormat); mimeType != "" {
		genConfig["responseMimeType"] = mimeType
		if schema != nil {
			genConfig["responseSchema"] = schema
		}
	}
	for k, v := range reqIR.Generation.Extra {
		if _, exists := genConfig[k]; !exists {
			genConfig[k] = v
		}
	}
	if len(genConfig) > 0 {
		out["generationConfig"] = genConfig
	}
	if len(reqIR.Safety.Settings) > 0 {
		out["safetySettings"] = relayir.CloneRaw(reqIR.Safety.Settings)
	}
	hasGeminiTools := false
	if len(reqIR.Tools) > 0 {
		tools := make([]schema.Tool, 0, len(reqIR.Tools))
		for _, tool := range reqIR.Tools {
			tools = append(tools, schemaToolFromIR(tool))
		}
		if projected := geminiTools(tools); len(projected) > 0 {
			out["tools"] = projected
			hasGeminiTools = true
		}
	}
	if hasGeminiTools && reqIR.ToolChoice != nil {
		if fcConfig, ok := geminiFunctionCallingConfig(reqIR.ToolChoice.Raw); ok {
			out["toolConfig"] = map[string]interface{}{"functionCallingConfig": fcConfig}
		}
	}
	for k, v := range reqIR.Metadata {
		if isGeminiEnvelopeProtocol(reqIR.SourceProtocol) && isGeminiCLIEnvelopeExtra(k) {
			continue
		}
		out[k] = v
	}
	for k, v := range reqIR.Native.Fields {
		if isGeminiEnvelopeProtocol(reqIR.SourceProtocol) && isGeminiCLIEnvelopeExtra(k) {
			continue
		}
		out[k] = v
	}
	return json.Marshal(out)
}

func geminiThinkingFromIRRequest(req *relayir.Request) interface{} {
	if req == nil {
		return nil
	}
	if thinking := normalizeAnyGeminiThinkingConfig(req.Generation.Thinking); thinking != nil {
		return thinking
	}
	return geminiThinkingFromReasoning(req.Generation.Reasoning)
}

func isGeminiEnvelopeProtocol(protocol relayir.Protocol) bool {
	return protocol == relayir.ProtocolGeminiCLI || protocol == relayir.ProtocolGeminiCode || protocol == relayir.ProtocolAntigravity
}

func geminiPartsFromIRTurn(source relayir.Protocol, turn relayir.Turn, names map[string]string) ([]map[string]interface{}, error) {
	parts := make([]map[string]interface{}, 0, len(turn.Items))
	for _, item := range turn.Items {
		if (source == relayir.ProtocolGemini || isGeminiEnvelopeProtocol(source)) && len(item.Native.Raw) > 0 {
			var raw map[string]interface{}
			if json.Unmarshal(item.Native.Raw, &raw) == nil {
				parts = append(parts, raw)
				continue
			}
		}
		part, err := geminiPartFromIRItem(item, names)
		if err != nil {
			return nil, err
		}
		if part != nil {
			parts = append(parts, part)
		}
	}
	return parts, nil
}

func geminiPartFromIRItem(item relayir.Item, names map[string]string) (map[string]interface{}, error) {
	switch item.Kind {
	case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
		return geminiReasoningPart(schemaReasoningFromIR(item)), nil
	case relayir.ItemToolUse, relayir.ItemFunctionCall:
		part := geminiToolCallPart(schemaToolCallFromIR(item))
		call := part["functionCall"].(map[string]interface{})
		if call["name"] == "" {
			return nil, fmt.Errorf("cannot emit Gemini functionCall for IR item %d: missing required name", item.OriginalIndex)
		}
		return part, nil
	case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
		part := geminiToolResultPart(schemaToolResultFromIR(item), names)
		resp := part["functionResponse"].(map[string]interface{})
		if resp["name"] == "" {
			return nil, fmt.Errorf("cannot emit Gemini functionResponse for IR item %d: missing required name", item.OriginalIndex)
		}
		return part, nil
	default:
		if part, ok := schemaContentFromIR(item); ok {
			return geminiContentPart(part), nil
		}
	}
	return nil, nil
}

func toolCallNameByIDIR(turns []relayir.Turn) map[string]string {
	names := map[string]string{}
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.ToolUse == nil {
				continue
			}
			id := firstNonEmptyString(item.ToolUse.CallID, item.ToolUse.ID, item.CallID, item.ID)
			if id != "" && item.ToolUse.Name != "" {
				names[id] = item.ToolUse.Name
			}
		}
	}
	return names
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
