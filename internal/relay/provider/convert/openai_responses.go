package convert

import (
	"encoding/json"
	"fmt"
	"time"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseOpenAIResponsesRequestDirectIR(body []byte) (*relayir.Request, error) {
	var req schema.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses request: %w", err)
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(body, &rawRoot)

	out := &relayir.Request{
		SourceProtocol: relayir.ProtocolOpenAIResponses,
		Model:          req.Model,
		Stream:         req.Stream,
		Native: relayir.NativeEnvelope{
			Protocol: relayir.ProtocolOpenAIResponses,
			RawBody:  relayir.CloneRaw(body),
		},
		Metadata: map[string]json.RawMessage{},
	}
	for k, v := range req.Extra {
		out.Metadata[k] = relayir.CloneRaw(v)
	}
	copyRawFields(out.Metadata, rawRoot,
		"truncation",
		"stream_options",
		"metadata",
		"user",
		"previous_response_id",
		"include",
		"text",
		"max_tool_calls",
		"conversation",
		"prompt_cache_key",
		"client_metadata",
		"safety_identifier",
	)
	out.Native.Fields = relayir.CloneRawMap(out.Metadata)
	out.Native.Unknown = relayir.CloneRawMap(out.Metadata)

	if req.Instructions != "" {
		out.Instructions = append(out.Instructions, relayir.Instruction{
			Role: relayir.RoleSystem,
			Text: req.Instructions,
			Items: []relayir.Item{{
				Kind: relayir.ItemText,
				Text: &relayir.Text{Text: req.Instructions},
			}},
		})
	}

	if req.Input.Text != nil {
		out.Turns = append(out.Turns, relayir.Turn{
			Role: relayir.RoleUser,
			Items: []relayir.Item{{
				Kind: relayir.ItemText,
				Text: &relayir.Text{Text: *req.Input.Text},
			}},
		})
	} else {
		for _, item := range req.Input.Items {
			itemType := effectiveResponsesInputItemType(item)
			if !knownResponsesInputItemType(itemType) {
				out.Losses = append(out.Losses, irloss(FormatOpenAIResponses, "", "$.input[]", itemType, item.Raw, "Responses input item is preserved as native raw/opaque IR"))
			}
			out.Turns = append(out.Turns, responsesInputItemToIRTurn(item))
		}
	}

	out.Generation.MaxTokens = req.MaxOutputTokens
	out.Generation.Temperature = req.Temperature
	out.Generation.TopP = req.TopP
	if _, ok := rawRoot["parallel_tool_calls"]; ok {
		out.Generation.ParallelToolCalls = &req.ParallelToolCalls
	} else if req.ParallelToolCalls {
		out.Generation.ParallelToolCalls = &req.ParallelToolCalls
	}
	out.Generation.ServiceTier = req.ServiceTier
	if _, ok := rawRoot["store"]; ok {
		out.Generation.Store = &req.Store
	} else if req.Store {
		out.Generation.Store = &req.Store
	}
	out.Generation.Reasoning = relayir.CloneRaw(req.Reasoning)
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			for _, tool := range tools {
				out.Tools = append(out.Tools, irTool(tool, FormatOpenAIResponses))
			}
		}
	}
	if req.ToolChoice != nil {
		out.ToolChoice = &relayir.ToolChoice{Raw: relayir.CloneRaw(req.ToolChoice)}
	}
	return out, nil
}

func knownResponsesInputItemType(typ string) bool {
	switch typ {
	case "message", "reasoning", "function_call", "function_call_output",
		"custom_tool_call", "custom_tool_call_output":
		return true
	default:
		return false
	}
}

func effectiveResponsesInputItemType(item schema.ResponsesInputItem) string {
	if item.Type != "" {
		return item.Type
	}
	if item.Role != "" || item.Content.Text != nil || len(item.Content.Parts) > 0 {
		return "message"
	}
	return ""
}

func responsesInputItemToIRTurn(item schema.ResponsesInputItem) relayir.Turn {
	rawItem := relayir.CloneRaw(item.Raw)
	itemType := effectiveResponsesInputItemType(item)
	turn := relayir.Turn{
		Role:     relayir.Role(responsesRole(item.Role)),
		ID:       item.ID,
		Status:   item.Status,
		Phase:    item.Phase,
		Metadata: relayir.CloneRawMap(item.Extra),
		Native: relayir.NativeEnvelope{
			Protocol: relayir.ProtocolOpenAIResponses,
			Kind:     itemType,
			Raw:      rawItem,
			Fields:   relayir.CloneRawMap(item.Extra),
		},
	}
	switch itemType {
	case "message":
		var content []schema.ContentPart
		if item.Content.Text != nil {
			content = []schema.ContentPart{{Type: "text", Text: *item.Content.Text}}
		} else if len(item.Content.Parts) > 0 {
			content = item.Content.Parts
		}
		for idx, part := range content {
			turn.Items = append(turn.Items, irContentPartItem(contentItemKindContent, part, rawJSON(part), FormatOpenAIResponses, idx))
		}
	case "reasoning":
		turn.Role = relayir.RoleAssistant
		for idx, part := range reasoningPartsFromResponsesExtra(item.Extra) {
			turn.Items = append(turn.Items, irContentPartItem(contentItemKindReasoning, part, rawItem, FormatOpenAIResponses, idx))
		}
	case "function_call":
		turn.Role = relayir.RoleAssistant
		name := qualifyResponsesNamespaceToolName(rawString(item.Extra["namespace"]), item.Name)
		call := schema.ToolCall{
			ID:   item.CallID,
			Type: "function",
			Name: name,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      name,
				Arguments: item.Arguments,
			},
		}
		turn.Items = append(turn.Items, irToolUseItem(call, rawItem, FormatOpenAIResponses, 0))
	case "function_call_output":
		turn.Role = relayir.RoleTool
		turn.Items = append(turn.Items, irToolResultItem(schema.ToolResult{
			ToolCallID: item.CallID,
			Content:    item.Output,
			ContentRaw: item.OutputRaw,
		}, rawItem, FormatOpenAIResponses, 0))
	case "custom_tool_call":
		// Codex custom tool call: has call_id, name, input fields
		turn.Role = relayir.RoleAssistant
		callID := rawString(item.Extra["call_id"])
		name := rawString(item.Extra["name"])
		inputRaw := item.Extra["input"]
		var arguments string
		if inputRaw != nil {
			arguments = string(inputRaw)
		}
		call := schema.ToolCall{
			ID:   callID,
			Type: "function",
			Name: name,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      name,
				Arguments: arguments,
			},
		}
		turn.Items = append(turn.Items, irToolUseItem(call, rawItem, FormatOpenAIResponses, 0))
	case "custom_tool_call_output":
		// Codex custom tool call output: has call_id, output fields
		turn.Role = relayir.RoleTool
		callID := rawString(item.Extra["call_id"])
		outputRaw := item.Extra["output"]
		var output string
		if outputRaw != nil {
			output = string(outputRaw)
		}
		turn.Items = append(turn.Items, irToolResultItem(schema.ToolResult{
			ToolCallID: callID,
			Content:    output,
			ContentRaw: outputRaw,
		}, rawItem, FormatOpenAIResponses, 0))
	default:
		turn.Role = relayir.RoleOpaque
		turn.Items = append(turn.Items, relayir.Item{
			ID:     item.ID,
			Kind:   relayir.ItemOpaque,
			Opaque: &relayir.Opaque{Type: itemType, Raw: rawItem},
			Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Kind: itemType, Raw: rawItem},
		})
	}
	if len(turn.Items) == 0 && len(rawItem) > 0 {
		turn.Items = append(turn.Items, relayir.Item{
			ID:     item.ID,
			Kind:   relayir.ItemOpaque,
			Opaque: &relayir.Opaque{Type: itemType, Raw: rawItem},
			Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Kind: itemType, Raw: rawItem},
		})
	}
	return turn
}

func emitOpenAIResponsesRequestDirectIR(req *relayir.Request) ([]byte, error) {
	resp := make(map[string]interface{})
	resp["model"] = req.Model
	resp["stream"] = req.Stream
	if len(req.Instructions) > 0 {
		resp["instructions"] = instructionTextForTarget(req.Instructions[0])
	} else {
		resp["instructions"] = ""
	}
	input := make([]map[string]interface{}, 0)
	for _, turn := range req.Turns {
		if isResponsesFamily(protocolFormat(req.SourceProtocol)) && len(turn.Native.Raw) > 0 {
			var raw map[string]interface{}
			if err := decodeJSONUseNumber(turn.Native.Raw, &raw); err == nil {
				normalizeResponsesRawInputItemForUpstream(raw)
				input = append(input, raw)
				continue
			}
		}
		items, err := responsesInputItemsFromIRTurn(turn)
		if err != nil {
			return nil, err
		}
		input = append(input, items...)
	}
	resp["input"] = input
	if req.Generation.MaxTokens != nil {
		resp["max_output_tokens"] = *req.Generation.MaxTokens
	}
	if req.Generation.Temperature != nil {
		resp["temperature"] = *req.Generation.Temperature
	}
	if req.Generation.TopP != nil {
		resp["top_p"] = *req.Generation.TopP
	}
	if req.Generation.ParallelToolCalls != nil {
		resp["parallel_tool_calls"] = *req.Generation.ParallelToolCalls
	}
	if req.Generation.ServiceTier != "" {
		resp["service_tier"] = req.Generation.ServiceTier
	}
	if req.Generation.Store != nil {
		resp["store"] = *req.Generation.Store
	}
	if req.Generation.Reasoning != nil {
		resp["reasoning"] = req.Generation.Reasoning
	}
	if req.Tools != nil {
		tools := make([]schema.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, schemaToolFromIR(tool))
		}
		raw, _ := json.Marshal(openAIResponsesTools(tools))
		resp["tools"] = json.RawMessage(raw)
		if req.ToolChoice == nil {
			resp["tool_choice"] = "auto"
		}
		if req.Generation.ParallelToolCalls == nil {
			resp["parallel_tool_calls"] = true
		}
	}
	if req.ToolChoice != nil {
		resp["tool_choice"] = relayir.CloneRaw(req.ToolChoice.Raw)
	}
	for k, v := range req.Metadata {
		resp[k] = v
	}
	if len(req.Native.Fields) > 0 {
		for k, v := range req.Native.Fields {
			resp[k] = v
		}
	}
	return json.Marshal(resp)
}

func openAIResponsesTools(tools []schema.Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		normalized := openAIResponsesTool(tool)
		if normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func openAIResponsesTool(tool schema.Tool) map[string]interface{} {
	name, description, parameters := normalizedFunctionTool(tool)
	if name != "" {
		out := map[string]interface{}{
			"type": "function",
			"name": name,
		}
		if description != "" {
			out["description"] = description
		}
		if len(parameters) > 0 && string(parameters) != "null" {
			out["parameters"] = json.RawMessage(parameters)
		} else {
			out["parameters"] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		for key, value := range tool.Extra {
			out[key] = value
		}
		if tool.Function != nil {
			for key, value := range tool.Function.Extra {
				out[key] = value
			}
		}
		return out
	}

	toolType := tool.Type
	if toolType == "" {
		return nil
	}
	out := map[string]interface{}{"type": toolType}
	if tool.Name != "" {
		out["name"] = tool.Name
	}
	if tool.Description != "" {
		out["description"] = tool.Description
	}
	if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
		out["parameters"] = json.RawMessage(tool.Parameters)
	}
	for key, value := range tool.Extra {
		out[key] = value
	}
	return out
}

func isResponsesFamily(format Format) bool {
	return format == FormatOpenAIResponses || format == FormatCodexResponses
}

func responsesInputItemsFromIRTurn(turn relayir.Turn) ([]map[string]interface{}, error) {
	var items []map[string]interface{}
	var pendingContent []schema.ContentPart
	role := responsesRole(string(turn.Role))
	flushContent := func() {
		if len(pendingContent) == 0 {
			return
		}
		if role == "assistant" && isOnlyEmptyResponsesTextParts(pendingContent) {
			pendingContent = nil
			return
		}
		item := map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": responsesMessageContent(role, pendingContent),
		}
		if turn.ID != "" {
			item["id"] = turn.ID
		}
		if turn.Status != "" {
			item["status"] = turn.Status
		}
		if turn.Phase != "" {
			item["phase"] = turn.Phase
		}
		for k, v := range responsesMessageMetadata(turn.Metadata) {
			item[k] = v
		}
		items = append(items, item)
		pendingContent = nil
	}
	for _, item := range turn.Items {
		switch item.Kind {
		case relayir.ItemText, relayir.ItemImage, relayir.ItemFile, relayir.ItemDocument, relayir.ItemAudio, relayir.ItemRefusal, relayir.ItemExecutableCode, relayir.ItemCodeExecutionResult, relayir.ItemOpaque:
			if role != "tool" {
				if part, ok := schemaContentFromIR(item); ok {
					pendingContent = append(pendingContent, part)
				}
			}
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			flushContent()
			part := schemaReasoningFromIR(item)
			reasoning := map[string]interface{}{
				"type":    "reasoning",
				"id":      generateResponsesReasoningID(),
				"summary": responsesReasoningSummary([]schema.ContentPart{part}),
			}
			if turn.ID != "" {
				reasoning["id"] = turn.ID
			}
			if turn.Status != "" {
				reasoning["status"] = turn.Status
			}
			if encrypted := reasoningEncryptedContent([]schema.ContentPart{part}); encrypted != "" {
				reasoning["encrypted_content"] = encrypted
			}
			items = append(items, reasoning)
		case relayir.ItemToolUse, relayir.ItemFunctionCall:
			flushContent()
			call := schemaToolCallFromIR(item)
			name := call.Name
			if name == "" {
				name = call.Function.Name
			}
			callID := firstNonEmptyString(call.ID, item.CallID, item.ID)
			if name == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call for IR item %d: missing required name", item.OriginalIndex)
			}
			if callID == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call for IR item %d: missing required call_id", item.OriginalIndex)
			}
			items = append(items, map[string]interface{}{
				"type":      "function_call",
				"call_id":   callID,
				"name":      name,
				"arguments": call.Function.Arguments,
			})
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			flushContent()
			result := schemaToolResultFromIR(item)
			if result.ToolCallID == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call_output for IR item %d: missing required call_id", item.OriginalIndex)
			}
			items = append(items, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": result.ToolCallID,
				"output":  responsesToolResultOutput(result),
			})
		}
	}
	flushContent()
	return items, nil
}

func isOnlyEmptyResponsesTextParts(parts []schema.ContentPart) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "output_text":
			if part.Text != "" || len(part.Extra) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func responsesRole(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "function":
		return "tool"
	case "unknown", "opaque", "":
		return "user"
	default:
		return role
	}
}

func responsesToolResultOutput(result schema.ToolResult) interface{} {
	if len(result.ContentRaw) > 0 {
		if text, ok := textOnlyContentBlocksString(result.ContentRaw); ok {
			return text
		}
		var value interface{}
		if err := json.Unmarshal(result.ContentRaw, &value); err == nil {
			return value
		}
	}
	return result.Content
}

func normalizeResponsesRawInputItemForUpstream(item map[string]interface{}) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		normalizeResponsesRawMessageContent(item)
	case "function_call_output", "custom_tool_call_output":
		normalizeResponsesRawFunctionCallOutput(item)
	case "custom_tool_call":
		// Normalize custom_tool_call input field to string if it's an object
		inputRaw, ok := item["input"]
		if !ok {
			return
		}
		if _, ok := inputRaw.(string); !ok {
			// Convert object to JSON string
			if inputBytes, err := json.Marshal(inputRaw); err == nil {
				item["input"] = string(inputBytes)
			}
		}
	}
}

func normalizeResponsesRawMessageContent(item map[string]interface{}) {
	role, _ := item["role"].(string)
	role = responsesRole(role)
	content, ok := item["content"].([]interface{})
	if !ok {
		return
	}
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		if partType != "text" {
			continue
		}
		if role == "assistant" {
			part["type"] = "output_text"
		} else {
			part["type"] = "input_text"
		}
	}
}

func normalizeResponsesRawFunctionCallOutput(item map[string]interface{}) {
	rawOutput, ok := item["output"]
	if !ok {
		return
	}
	if text, ok := textOnlyInterfaceContentBlocksString(rawOutput); ok {
		item["output"] = text
	}
}

func textOnlyContentBlocksString(raw json.RawMessage) (string, bool) {
	var blocks []interface{}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false
	}
	return textOnlyInterfaceContentBlocksString(blocks)
}

func textOnlyInterfaceContentBlocksString(value interface{}) (string, bool) {
	blocks, ok := value.([]interface{})
	if !ok || len(blocks) == 0 {
		return "", false
	}
	texts := make([]string, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]interface{})
		if !ok {
			return "", false
		}
		blockType, _ := block["type"].(string)
		if blockType != "text" && blockType != "input_text" && blockType != "output_text" {
			return "", false
		}
		text, _ := block["text"].(string)
		texts = append(texts, text)
	}
	return joinNonEmpty(texts, "\n"), true
}

func generateResponsesReasoningID() string {
	return fmt.Sprintf("rs_%x", time.Now().UnixNano())
}

func responsesMessageMetadata(metadata map[string]json.RawMessage) map[string]json.RawMessage {
	if len(metadata) == 0 {
		return nil
	}
	out := relayir.CloneRawMap(metadata)
	for _, key := range []string{"reasoning_content", "reasoning", "reasoning_details"} {
		delete(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyRawFields(dst map[string]json.RawMessage, src map[string]json.RawMessage, keys ...string) {
	if dst == nil || src == nil {
		return
	}
	for _, key := range keys {
		if raw, ok := src[key]; ok {
			dst[key] = append(json.RawMessage(nil), raw...)
		}
	}
}

func responsesMessageContent(role string, content []schema.ContentPart) interface{} {
	if len(content) == 1 && role != "assistant" {
		part := content[0]
		if (part.Type == "text" || part.Type == "input_text") && len(part.Extra) == 0 {
			return part.Text
		}
	}

	parts := make([]map[string]interface{}, 0, len(content))
	for _, part := range content {
		parts = append(parts, responsesContentPartMap(role, part))
	}
	return parts
}

func responsesContentPartMap(role string, part schema.ContentPart) map[string]interface{} {
	partType := part.Type
	switch partType {
	case "text":
		if role == "assistant" {
			partType = "output_text"
		} else {
			partType = "input_text"
		}
	case "image_url":
		partType = "input_image"
	case "file":
		partType = "input_file"
	}
	out := make(map[string]interface{}, len(part.Extra)+6)
	if partType != "input_file" {
		for key, value := range part.Extra {
			if key == "cache_control" {
				continue
			}
			out[key] = value
		}
	}
	if partType != "" {
		out["type"] = partType
	}
	if part.Text != "" || partType == "input_text" || partType == "output_text" || partType == "text" {
		out["text"] = part.Text
	}
	if part.ImageURL != nil {
		out["image_url"] = *part.ImageURL
	}
	if part.ImageDetail != "" {
		out["detail"] = part.ImageDetail
	}
	if part.FileData != "" {
		out["file_data"] = part.FileData
	}
	if part.FileURL != "" {
		out["file_url"] = part.FileURL
	}
	if part.FileID != "" {
		out["file_id"] = part.FileID
	}
	if part.Filename != "" {
		out["filename"] = part.Filename
	} else if partType == "input_file" && part.FileData != "" {
		out["filename"] = responsesDefaultFilename(part)
	}
	if part.FileType != "" && partType != "input_file" {
		out["file_type"] = part.FileType
	}
	if part.Data != "" {
		out["data"] = part.Data
	}
	if part.MimeType != "" && partType != "input_file" {
		out["mime_type"] = part.MimeType
	}
	if part.Refusal != "" {
		out["refusal"] = part.Refusal
	}
	return out
}

func responsesDefaultFilename(part schema.ContentPart) string {
	mimeType := firstNonEmptyString(part.FileType, part.MimeType)
	switch mimeType {
	case "application/pdf":
		return "input.pdf"
	case "text/plain":
		return "input.txt"
	case "text/csv":
		return "input.csv"
	case "application/json":
		return "input.json"
	case "text/markdown":
		return "input.md"
	default:
		return "input.bin"
	}
}

func init() {
	registerRequestIRParser(FormatOpenAIResponses, parseOpenAIResponsesRequestIR)
	registerRequestIREmitter(FormatOpenAIResponses, emitOpenAIResponsesRequestIR)
}
