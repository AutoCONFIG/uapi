package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseAnthropicRequestDirectIR(body []byte) (*relayir.Request, error) {
	var req schema.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}
	out := &relayir.Request{
		SourceProtocol: relayir.ProtocolAnthropic,
		Model:          req.Model,
		Stream:         req.Stream,
		Metadata:       relayir.CloneRawMap(req.Extra),
		Native:         relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, RawBody: relayir.CloneRaw(body), Fields: relayir.CloneRawMap(req.Extra), Unknown: relayir.CloneRawMap(req.Extra)},
	}
	if len(req.Metadata) > 0 {
		if out.Metadata == nil {
			out.Metadata = map[string]json.RawMessage{}
		}
		out.Metadata["metadata"] = relayir.CloneRaw(req.Metadata)
		out.Native.Fields = relayir.CloneRawMap(out.Metadata)
		out.Native.Unknown = relayir.CloneRawMap(out.Metadata)
	}
	if len(req.System) > 0 {
		out.Instructions = append(out.Instructions, anthropicSystemInstruction(req.System))
	}
	for _, msg := range req.Messages {
		out.Turns = append(out.Turns, anthropicMessageToIRTurn(msg))
	}
	out.Generation.MaxTokens = req.MaxTokens
	out.Generation.Temperature = req.Temperature
	out.Generation.TopP = req.TopP
	out.Generation.TopK = req.TopK
	out.Generation.Stop = append([]string(nil), req.StopSequences...)
	out.Generation.Thinking = relayir.CloneRaw(req.Thinking)
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			for _, tool := range tools {
				out.Tools = append(out.Tools, irTool(tool, FormatAnthropic))
			}
		}
	}
	if req.ToolChoice != nil {
		out.ToolChoice = &relayir.ToolChoice{Raw: relayir.CloneRaw(req.ToolChoice)}
	}
	return out, nil
}

func anthropicSystemInstruction(raw json.RawMessage) relayir.Instruction {
	inst := relayir.Instruction{Role: relayir.RoleSystem, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Kind: "system", Raw: relayir.CloneRaw(raw)}}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		inst.Text = text
		inst.Items = []relayir.Item{{Kind: relayir.ItemText, Text: &relayir.Text{Text: text}}}
		return inst
	}
	var blocks []schema.AnthropicContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var texts []string
		for idx, block := range blocks {
			item := anthropicBlockToIRItem(block, idx)
			inst.Items = append(inst.Items, item)
			if item.Text != nil && item.Text.Text != "" {
				texts = append(texts, item.Text.Text)
			}
		}
		inst.Text = joinNonEmpty(texts, "\n\n")
	}
	return inst
}

func anthropicMessageToIRTurn(msg schema.AnthropicMessage) relayir.Turn {
	turn := relayir.Turn{Role: relayir.Role(anthropicRole(msg.Role)), Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Kind: "message", Raw: rawJSON(msg)}}
	for idx, block := range msg.Content {
		turn.Items = append(turn.Items, anthropicBlockToIRItem(block, idx))
	}
	return turn
}

func anthropicBlockToIRItem(block schema.AnthropicContentBlock, idx int) relayir.Item {
	rawBlock := rawJSON(block)
	switch block.Type {
	case "text":
		return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: "text", Text: block.Text, Extra: block.Extra}, rawBlock, FormatAnthropic, idx)
	case "image":
		if block.Source == nil {
			return relayir.Item{Kind: relayir.ItemOpaque, OriginalIndex: idx, Opaque: &relayir.Opaque{Type: block.Type, Raw: rawBlock}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Kind: block.Type, Raw: rawBlock, Index: idx}}
		}
		part := schema.ContentPart{Type: "image_url", MimeType: block.Source.MediaType, Extra: block.Extra}
		switch block.Source.Type {
		case "url":
			part.ImageURL = stringPtr(block.Source.URL)
		default:
			dataURI := fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
			part.ImageURL = &dataURI
		}
		return irContentPartItem(contentItemKindContent, part, rawBlock, FormatAnthropic, idx)
	case "document":
		part := anthropicDocumentPart(block)
		return irContentPartItem(contentItemKindContent, part, rawBlock, FormatAnthropic, idx)
	case "tool_use":
		return irToolUseItem(schema.ToolCall{ID: block.ID, Type: "function", Name: block.Name, Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: block.Name, Arguments: rawJSONArgumentString(block.Input)}}, rawBlock, FormatAnthropic, idx)
	case "tool_result":
		return irToolResultItem(schema.ToolResult{ToolCallID: block.ToolUseID, Content: anthropicToolResultContentString(block.Content), ContentRaw: relayir.CloneRaw(block.Content), IsError: block.IsError}, rawBlock, FormatAnthropic, idx)
	case "thinking":
		extra := map[string]json.RawMessage{}
		if block.Signature != "" {
			extra = setRawString(extra, reasoningExtraSignature, block.Signature)
		}
		for k, v := range block.Extra {
			extra[k] = v
		}
		return irContentPartItem(contentItemKindReasoning, schema.ContentPart{Type: "thinking", Text: block.Thinking, Extra: extra}, rawBlock, FormatAnthropic, idx)
	case "redacted_thinking":
		if raw, ok := block.Extra[reasoningExtraData]; ok && rawString(raw) != "" {
			return irContentPartItem(contentItemKindReasoning, reasoningPartWithExtra("", map[string]json.RawMessage{reasoningExtraData: raw, reasoningExtraEncryptedContent: raw, reasoningExtraType: json.RawMessage(`"reasoning.encrypted"`)}), rawBlock, FormatAnthropic, idx)
		}
	}
	return irContentPartItem(contentItemKindContent, schema.ContentPart{Type: block.Type, Text: block.Text, Extra: block.Extra}, rawBlock, FormatAnthropic, idx)
}

func anthropicDocumentPart(block schema.AnthropicContentBlock) schema.ContentPart {
	part := schema.ContentPart{Type: "file", Extra: block.Extra}
	if block.Source == nil {
		return part
	}
	part.FileType = block.Source.MediaType
	part.MimeType = block.Source.MediaType
	if title := rawString(block.Extra["title"]); title != "" {
		part.Filename = title
	}
	switch block.Source.Type {
	case "base64":
		if block.Source.MediaType != "" {
			part.FileData = fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
		} else {
			part.FileData = block.Source.Data
		}
	case "text":
		part.FileData = block.Source.Data
		if part.FileType == "" {
			part.FileType = "text/plain"
		}
	case "url":
		part.FileURL = block.Source.URL
	case "file":
		part.FileID = block.Source.FileID
	}
	return part
}

func emitAnthropicRequestDirectIR(req *relayir.Request) ([]byte, error) {
	out := make(map[string]interface{})
	out["model"] = req.Model
	out["max_tokens"] = 4096
	if req.Generation.MaxTokens != nil {
		out["max_tokens"] = *req.Generation.MaxTokens
	}
	out["stream"] = req.Stream
	for k, v := range req.Metadata {
		out[k] = v
	}
	if len(req.Instructions) > 0 {
		out["system"] = anthropicSystemFromIR(req.SourceProtocol, req.Instructions, req.Model)
	}
	messages := make([]map[string]interface{}, 0, len(req.Turns))
	for _, turn := range req.Turns {
		if turn.Role == relayir.RoleSystem || turn.Role == relayir.RoleDeveloper {
			out["system"] = appendAnthropicSystem(out["system"], anthropicSystemFromIR(req.SourceProtocol, []relayir.Instruction{anthropicInstructionFromTurn(turn)}, req.Model))
			continue
		}
		role := anthropicRole(string(turn.Role))
		if role == "tool" || role == "function" {
			role = "user"
		}
		msg := map[string]interface{}{"role": role}
		blocks, err := anthropicBlocksFromIRTurn(req.SourceProtocol, turn, req.Model)
		if err != nil {
			return nil, err
		}
		if len(blocks) > 0 {
			msg["content"] = blocks
		}
		messages = append(messages, msg)
	}
	out["messages"] = messages
	if req.Generation.Temperature != nil {
		out["temperature"] = *req.Generation.Temperature
	}
	if req.Generation.TopP != nil {
		out["top_p"] = *req.Generation.TopP
	}
	if req.Generation.TopK != nil {
		out["top_k"] = *req.Generation.TopK
	}
	if len(req.Generation.Stop) > 0 {
		out["stop_sequences"] = req.Generation.Stop
	}
	toolChoice := anthropicToolChoice(nilIfEmptyToolChoice(req.ToolChoice), req.Generation.ParallelToolCalls)
	forcedToolChoice := anthropicToolChoiceForcesToolUse(toolChoice)
	if forcedToolChoice {
		removeAnthropicForcedThinkingExtras(out)
	}
	if thinking := anthropicThinkingFromIRRequest(req); thinking != nil && !forcedToolChoice {
		out["thinking"] = thinking
	}
	if len(req.Tools) > 0 {
		tools := make([]schema.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, schemaToolFromIR(tool))
		}
		if projected := anthropicTools(tools, req.SourceProtocol == relayir.ProtocolAnthropic || req.SourceProtocol == relayir.ProtocolClaudeCode, req.Model); len(projected) > 0 {
			out["tools"] = projected
		}
	}
	if toolChoice != nil {
		out["tool_choice"] = toolChoice
	}
	for k, v := range req.Native.Fields {
		out[k] = relayir.CloneRaw(v)
	}
	if forcedToolChoice {
		removeAnthropicForcedThinkingExtras(out)
	}
	return json.Marshal(out)
}

func anthropicInstructionFromTurn(turn relayir.Turn) relayir.Instruction {
	inst := relayir.Instruction{
		Role:     relayir.RoleSystem,
		Items:    append([]relayir.Item(nil), turn.Items...),
		Name:     turn.Name,
		ID:       turn.ID,
		Metadata: relayir.CloneRawMap(turn.Metadata),
		Native:   turn.Native,
	}
	var texts []string
	for _, item := range turn.Items {
		if item.Text != nil && item.Text.Text != "" {
			texts = append(texts, item.Text.Text)
		}
	}
	inst.Text = joinNonEmpty(texts, "\n\n")
	return inst
}

func appendAnthropicSystem(existing interface{}, addition interface{}) interface{} {
	if addition == nil {
		return existing
	}
	if existing == nil {
		return addition
	}
	blocks := append(anthropicSystemBlocks(existing), anthropicSystemBlocks(addition)...)
	if len(blocks) == 0 {
		return existing
	}
	if len(blocks) == 1 {
		if typ, _ := blocks[0]["type"].(string); typ == "text" {
			if text, _ := blocks[0]["text"].(string); text != "" {
				return text
			}
		}
	}
	return blocks
}

func anthropicSystemBlocks(value interface{}) []map[string]interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []map[string]interface{}{{"type": "text", "text": v}}
	case []map[string]interface{}:
		return append([]map[string]interface{}(nil), v...)
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, cloneInterfaceMap(m))
			}
		}
		return out
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return anthropicSystemBlocks(text)
		}
		var blocks []map[string]interface{}
		if json.Unmarshal(raw, &blocks) == nil {
			return blocks
		}
	}
	return nil
}

func nilIfEmptyToolChoice(choice *relayir.ToolChoice) json.RawMessage {
	if choice == nil {
		return nil
	}
	return relayir.CloneRaw(choice.Raw)
}

func anthropicThinkingFromIRRequest(req *relayir.Request) interface{} {
	if req == nil {
		return nil
	}
	if thinking := anthropicThinkingFromRawThinking(req.Generation.Thinking); thinking != nil {
		return thinking
	}
	if thinking := anthropicThinkingFromReasoning(req.Generation.Reasoning); thinking != nil {
		return thinking
	}
	return anthropicThinkingFromGeminiThinking(req.Generation.Thinking)
}

func anthropicSystemFromIR(source relayir.Protocol, instructions []relayir.Instruction, model string) interface{} {
	if (source == relayir.ProtocolAnthropic || source == relayir.ProtocolClaudeCode) && len(instructions) == 1 && len(instructions[0].Native.Raw) > 0 {
		var raw interface{}
		if json.Unmarshal(instructions[0].Native.Raw, &raw) == nil {
			return raw
		}
	}
	var blocks []map[string]interface{}
	for _, inst := range instructions {
		for _, item := range inst.Items {
			if source != relayir.ProtocolAnthropic && isAnthropicTransportTextItem(item) {
				continue
			}
			block, err := anthropicBlockFromIRItem(item, model)
			if err != nil {
				continue
			}
			if block != nil {
				blocks = append(blocks, block)
			}
		}
		if len(inst.Items) == 0 && inst.Text != "" {
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": inst.Text})
		}
	}
	if len(blocks) == 1 {
		if text, ok := blocks[0]["text"].(string); ok && blocks[0]["type"] == "text" {
			return text
		}
	}
	return blocks
}

func anthropicToolChoice(raw json.RawMessage, parallelToolCalls *bool) map[string]interface{} {
	var out map[string]interface{}
	if len(raw) > 0 && string(raw) != "null" {
		var choice string
		if err := json.Unmarshal(raw, &choice); err == nil {
			switch choice {
			case "auto":
				out = map[string]interface{}{"type": "auto"}
			case "required":
				out = map[string]interface{}{"type": "any"}
			case "none":
				out = map[string]interface{}{"type": "none"}
			}
		} else {
			var choiceMap map[string]interface{}
			if err := json.Unmarshal(raw, &choiceMap); err == nil {
				choiceType, _ := choiceMap["type"].(string)
				switch choiceType {
				case "function":
					if name := toolChoiceFunctionName(choiceMap); name != "" {
						out = map[string]interface{}{"type": "tool", "name": name}
					}
				case "auto", "any", "none":
					out = map[string]interface{}{"type": choiceType}
				case "tool":
					out = cloneInterfaceMap(choiceMap)
				default:
					if choiceType != "" {
						out = cloneInterfaceMap(choiceMap)
					}
				}
			}
		}
	}
	if parallelToolCalls != nil {
		if out == nil {
			out = map[string]interface{}{"type": "auto"}
		}
		if out["type"] != "none" {
			out["disable_parallel_tool_use"] = !*parallelToolCalls
		}
	}
	return out
}

func toolChoiceFunctionName(choice map[string]interface{}) string {
	namespace := toolChoiceNamespace(choice)
	if function, ok := choice["function"].(map[string]interface{}); ok {
		if name, ok := function["name"].(string); ok {
			return qualifyResponsesNamespaceToolName(namespace, name)
		}
	}
	if name, ok := choice["name"].(string); ok {
		return qualifyResponsesNamespaceToolName(namespace, name)
	}
	return ""
}

func anthropicToolChoiceForcesToolUse(choice map[string]interface{}) bool {
	if choice == nil {
		return false
	}
	choiceType, _ := choice["type"].(string)
	return choiceType == "any" || choiceType == "tool"
}

func removeAnthropicForcedThinkingExtras(req map[string]interface{}) {
	outputConfig, ok := req["output_config"]
	if !ok {
		return
	}
	raw, ok := outputConfig.(json.RawMessage)
	if !ok {
		return
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return
	}
	delete(cfg, "effort")
	if len(cfg) == 0 {
		delete(req, "output_config")
		return
	}
	req["output_config"] = cfg
}

func cloneInterfaceMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func anthropicTools(tools []schema.Tool, preserveNative bool, model string) []map[string]interface{} {
	projected := tools
	if !preserveNative {
		projected = functionToolProjections(tools)
	}
	out := make([]map[string]interface{}, 0, len(projected))
	if !preserveNative {
		for _, tool := range tools {
			if isAnthropicWebSearchToolType(tool.Type) && shouldEmitAnthropicServerTools(model) {
				if normalized := anthropicTool(tool, false); normalized != nil {
					out = append(out, normalized)
				}
			}
		}
	}
	for _, tool := range projected {
		normalized := anthropicTool(tool, preserveNative)
		if normalized != nil {
			if preserveNative || !isAnthropicWebSearchToolType(tool.Type) {
				out = append(out, normalized)
			}
		}
	}
	return out
}

func anthropicTool(tool schema.Tool, preserveNative bool) map[string]interface{} {
	if isAnthropicWebSearchToolType(tool.Type) {
		return anthropicWebSearchTool(tool)
	}
	name, description, inputSchema := normalizedFunctionTool(tool)
	if name != "" {
		out := map[string]interface{}{
			"name": name,
		}
		if description != "" {
			out["description"] = description
		}
		if len(inputSchema) > 0 && string(inputSchema) != "null" {
			out["input_schema"] = json.RawMessage(inputSchema)
		} else {
			out["input_schema"] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		applyAnthropicCacheControl(out, tool.Extra)
		if preserveNative {
			for key, value := range tool.Extra {
				out[key] = value
			}
		}
		return out
	}
	if strings.TrimSpace(tool.Type) == "" || strings.TrimSpace(tool.Type) == "opaque" {
		return nil
	}
	out := map[string]interface{}{"type": strings.TrimSpace(tool.Type)}
	if tool.Name != "" {
		out["name"] = tool.Name
	}
	if tool.Description != "" {
		out["description"] = tool.Description
	}
	if len(tool.InputSchema) > 0 && string(tool.InputSchema) != "null" {
		out["input_schema"] = json.RawMessage(tool.InputSchema)
	}
	if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
		out["parameters"] = json.RawMessage(tool.Parameters)
	}
	for key, value := range tool.Extra {
		out[key] = value
	}
	return out
}

func isAnthropicWebSearchToolType(toolType string) bool {
	toolType = strings.TrimSpace(toolType)
	return toolType == "web_search" || strings.HasPrefix(toolType, "web_search_")
}

func shouldEmitAnthropicServerTools(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "glm-"):
		return false
	default:
		return true
	}
}

func anthropicWebSearchTool(tool schema.Tool) map[string]interface{} {
	if externalWebAccess := rawBool(tool.Extra["external_web_access"]); externalWebAccess != nil && !*externalWebAccess {
		return nil
	}
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		name = "web_search"
	}
	out := map[string]interface{}{
		"type": "web_search_20250305",
		"name": name,
	}
	if typ := strings.TrimSpace(tool.Type); strings.HasPrefix(typ, "web_search_") {
		out["type"] = typ
	}
	if raw := tool.Extra["max_uses"]; len(raw) > 0 && string(raw) != "null" {
		out["max_uses"] = json.RawMessage(raw)
	}
	if raw := tool.Extra["user_location"]; len(raw) > 0 && string(raw) != "null" {
		out["user_location"] = json.RawMessage(raw)
	}
	if raw := allowedDomainsFromToolFilters(tool.Extra["filters"]); len(raw) > 0 {
		out["allowed_domains"] = json.RawMessage(raw)
	}
	for key, value := range tool.Extra {
		switch key {
		case "external_web_access", "filters", "max_uses", "user_location":
			continue
		}
		out[key] = value
	}
	return out
}

func applyAnthropicCacheControl(out map[string]interface{}, metadata map[string]json.RawMessage) {
	if out == nil || len(metadata) == 0 {
		return
	}
	if raw := metadata["cache_control"]; len(raw) > 0 && string(raw) != "null" {
		out["cache_control"] = json.RawMessage(raw)
	}
}

func rawBool(raw json.RawMessage) *bool {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return &value
}

func allowedDomainsFromToolFilters(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var filters struct {
		AllowedDomains json.RawMessage `json:"allowed_domains"`
	}
	if err := json.Unmarshal(raw, &filters); err != nil {
		return nil
	}
	return filters.AllowedDomains
}

func anthropicRole(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "unknown", "opaque", "":
		return "user"
	default:
		return role
	}
}

func isAnthropicFamily(format Format) bool {
	return format == FormatAnthropic || format == FormatClaudeCode
}

func anthropicBlocksFromIRTurn(source relayir.Protocol, turn relayir.Turn, model string) ([]map[string]interface{}, error) {
	blocks := make([]map[string]interface{}, 0, len(turn.Items))
	for _, item := range turn.Items {
		if (source == relayir.ProtocolAnthropic || source == relayir.ProtocolClaudeCode) && len(item.Native.Raw) > 0 {
			var raw map[string]interface{}
			if json.Unmarshal(item.Native.Raw, &raw) == nil {
				if shouldDropAnthropicRedactedThinking(model, raw) {
					continue
				}
				blocks = append(blocks, raw)
				continue
			}
		}
		block, err := anthropicBlockFromIRItem(item, model)
		if err != nil {
			return nil, err
		}
		if block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

func anthropicBlockFromIRItem(item relayir.Item, model string) (map[string]interface{}, error) {
	switch item.Kind {
	case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
		block := anthropicReasoningBlock(schemaReasoningFromIR(item))
		if shouldDropAnthropicRedactedThinking(model, block) {
			return nil, nil
		}
		return block, nil
	case relayir.ItemToolUse, relayir.ItemFunctionCall:
		block := anthropicToolUseBlock(schemaToolCallFromIR(item))
		applyAnthropicCacheControl(block, item.Metadata)
		if block["name"] == "" {
			return nil, fmt.Errorf("cannot emit Anthropic tool_use for IR item %d: missing required name", item.OriginalIndex)
		}
		if block["id"] == "" {
			return nil, fmt.Errorf("cannot emit Anthropic tool_use for IR item %d: missing required id", item.OriginalIndex)
		}
		return block, nil
	case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
		block := anthropicToolResultBlock(schemaToolResultFromIR(item))
		applyAnthropicCacheControl(block, item.Metadata)
		if block["tool_use_id"] == "" {
			return nil, fmt.Errorf("cannot emit Anthropic tool_result for IR item %d: missing required tool_use_id", item.OriginalIndex)
		}
		return block, nil
	default:
		if part, ok := schemaContentFromIR(item); ok {
			return anthropicContentBlock(part), nil
		}
	}
	return nil, nil
}

func shouldDropAnthropicRedactedThinking(model string, block map[string]interface{}) bool {
	if block == nil || !isGLMAnthropicCompatModel(model) {
		return false
	}
	typ, _ := block["type"].(string)
	return typ == "redacted_thinking"
}

func isGLMAnthropicCompatModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "glm-")
}

func anthropicReasoningBlock(rc schema.ContentPart) map[string]interface{} {
	sig := reasoningSignature([]schema.ContentPart{rc})
	encrypted := reasoningPartEncryptedData(rc)
	if rc.Text == "" && sig == "" && encrypted == "" {
		return nil
	}
	if rc.Text == "" && encrypted != "" && sig == "" {
		return map[string]interface{}{
			"type": "redacted_thinking",
			"data": encrypted,
		}
	}
	block := map[string]interface{}{
		"type":     "thinking",
		"thinking": rc.Text,
	}
	if sig != "" {
		block["signature"] = sig
	}
	for k, v := range rc.Extra {
		if k == reasoningExtraSignature || k == reasoningExtraThoughtSignature || k == reasoningExtraEncryptedContent || k == reasoningExtraData || k == reasoningExtraType {
			continue
		}
		block[k] = v
	}
	return block
}

func anthropicContentBlock(c schema.ContentPart) map[string]interface{} {
	block := map[string]interface{}{}
	for k, v := range c.Extra {
		block[k] = v
	}
	switch c.Type {
	case "text":
		block["type"] = "text"
		block["text"] = c.Text
	case "image_url":
		if c.ImageURL == nil {
			return nil
		}
		dataURI := *c.ImageURL
		mediaType := "image/png"
		if c.MimeType != "" {
			mediaType = c.MimeType
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
			mediaType = dataURI[len("data:"):endIdx]
			if endIdx < len(dataURI) && dataURI[endIdx] == ';' {
				for i := endIdx + 1; i < len(dataURI); i++ {
					if dataURI[i] == ',' {
						data = dataURI[i+1:]
						break
					}
				}
			}
		}
		block["type"] = "image"
		block["source"] = map[string]string{
			"type":       "base64",
			"media_type": mediaType,
			"data":       data,
		}
	case "file", "input_file":
		mediaType := c.FileType
		if mediaType == "" {
			mediaType = c.MimeType
		}
		if mediaType == "" {
			mediaType = mimeTypeFromFilename(c.Filename)
		}
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		block["type"] = "document"
		if c.Filename != "" {
			block["title"] = c.Filename
		}
		switch {
		case c.FileURL != "":
			block["source"] = map[string]string{
				"type": "url",
				"url":  strings.TrimPrefix(c.FileURL, "file://"),
			}
		case c.FileID != "":
			block["source"] = map[string]string{
				"type":    "file",
				"file_id": c.FileID,
			}
		case c.FileData != "":
			data := c.FileData
			if strings.HasPrefix(data, "data:") {
				parsedMime, parsedData, ok := splitDataURI(data)
				if ok {
					mediaType = parsedMime
					data = parsedData
				}
			}
			block["source"] = map[string]string{
				"type":       "base64",
				"media_type": mediaType,
				"data":       data,
			}
		default:
			return nil
		}
	default:
		if c.Type == "" {
			return nil
		}
		block["type"] = c.Type
		if c.Text != "" {
			block["text"] = c.Text
		}
	}
	return block
}

func anthropicToolUseBlock(tc schema.ToolCall) map[string]interface{} {
	name := tc.Name
	if name == "" {
		name = tc.Function.Name
	}
	return map[string]interface{}{
		"type":  "tool_use",
		"id":    tc.ID,
		"name":  name,
		"input": jsonArgumentValue(tc.Function.Arguments),
	}
}

func anthropicToolResultBlock(result schema.ToolResult) map[string]interface{} {
	block := map[string]interface{}{
		"type":        "tool_result",
		"tool_use_id": result.ToolCallID,
		"content":     result.Content,
	}
	if result.IsError {
		block["is_error"] = true
	}
	return block
}

func anthropicToolResultContentString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "")
		}
	}
	return string(raw)
}

func init() {
	registerRequestIRParser(FormatAnthropic, parseAnthropicRequestIR)
	registerRequestIREmitter(FormatAnthropic, emitAnthropicRequestIR)
}
