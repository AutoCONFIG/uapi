package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// parseAnthropicRequest converts Anthropic Messages API request to an adapter request.
func parseAnthropicRequest(body []byte) (*adapterRequest, error) {
	var req schema.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}

	ir := &adapterRequest{
		Model:        req.Model,
		Stream:       req.Stream,
		SourceFormat: FormatAnthropic,
		Extra:        make(map[string]json.RawMessage),
		MaxTokens:    req.MaxTokens,
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
	}
	if len(req.Metadata) > 0 {
		ir.Extra["metadata"] = append(json.RawMessage(nil), req.Metadata...)
	}

	// Extract system message to Instructions
	if req.System != nil {
		ir.InstructionsRaw = append(json.RawMessage(nil), req.System...)
		var sysStr string
		if err := json.Unmarshal(req.System, &sysStr); err == nil {
			ir.Instructions = &sysStr
		} else {
			// Try as array of text blocks
			var blocks []map[string]string
			if err := json.Unmarshal(req.System, &blocks); err == nil {
				var texts []string
				for _, b := range blocks {
					if t, ok := b["text"]; ok {
						texts = append(texts, t)
					}
				}
				if len(texts) > 0 {
					instr := joinNonEmpty(texts, "\n\n")
					ir.Instructions = &instr
				}
			}
		}
	}

	// Convert messages
	for _, msg := range req.Messages {
		requestMsg := adapterTurn{
			Role: msg.Role,
		}

		// Convert content blocks
		for _, block := range msg.Content {
			rawBlock := rawJSON(block)
			switch block.Type {
			case "text":
				part := schema.ContentPart{
					Type:  "text",
					Text:  block.Text,
					Extra: block.Extra,
				}
				appendContentItem(&requestMsg, part, rawBlock)
			case "image":
				if block.Source != nil {
					part := schema.ContentPart{
						Type:     "image_url",
						MimeType: block.Source.MediaType,
						Extra:    block.Extra,
					}
					switch block.Source.Type {
					case "url":
						part.ImageURL = stringPtr(block.Source.URL)
					default:
						dataURI := fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
						part.ImageURL = &dataURI
					}
					if part.ImageURL != nil && *part.ImageURL != "" {
						appendContentItem(&requestMsg, part, rawBlock)
					}
				}
			case "document":
				if block.Source != nil {
					part := schema.ContentPart{
						Type:     "file",
						FileType: block.Source.MediaType,
						MimeType: block.Source.MediaType,
						Extra:    block.Extra,
					}
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
					appendContentItem(&requestMsg, part, rawBlock)
				}
			case "tool_use":
				args := rawJSONArgumentString(block.Input)
				call := schema.ToolCall{
					ID:   block.ID,
					Type: "function",
					Name: block.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      block.Name,
						Arguments: args,
					},
				}
				appendToolCallItem(&requestMsg, call, rawBlock)
			case "tool_result":
				appendToolResultItem(&requestMsg, schema.ToolResult{
					ToolCallID: block.ToolUseID,
					Content:    anthropicToolResultContentString(block.Content),
					IsError:    block.IsError,
				}, rawBlock)
			case "thinking":
				extra := map[string]json.RawMessage{}
				if block.Signature != "" {
					extra = setRawString(extra, reasoningExtraSignature, block.Signature)
				}
				for k, v := range block.Extra {
					extra[k] = v
				}
				appendReasoningItem(&requestMsg, schema.ContentPart{
					Type:  "thinking",
					Text:  block.Thinking,
					Extra: extra,
				}, rawBlock)
			case "redacted_thinking":
				if raw, ok := block.Extra[reasoningExtraData]; ok && rawString(raw) != "" {
					appendReasoningItem(&requestMsg, reasoningPartWithExtra("", map[string]json.RawMessage{
						reasoningExtraData:             raw,
						reasoningExtraEncryptedContent: raw,
						reasoningExtraType:             json.RawMessage(`"reasoning.encrypted"`),
					}), rawBlock)
				}
			default:
				appendContentItem(&requestMsg, schema.ContentPart{
					Type:  block.Type,
					Text:  block.Text,
					Extra: block.Extra,
				}, rawBlock)
			}
		}

		ir.Messages = append(ir.Messages, requestMsg)
	}

	// Generation parameters
	if req.Temperature != nil {
		ir.Temperature = req.Temperature
	}
	if req.TopP != nil {
		ir.TopP = req.TopP
	}
	if req.TopK != nil {
		ir.TopK = req.TopK
	}
	if len(req.StopSequences) > 0 {
		ir.StopWords = req.StopSequences
	}
	if req.Thinking != nil {
		ir.Thinking = req.Thinking
	}
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			ir.Tools = tools
		}
	}
	if req.ToolChoice != nil {
		ir.ToolChoice = req.ToolChoice
	}

	return ir, nil
}

// emitAnthropicRequest converts an adapter request to Anthropic Messages API request.
func emitAnthropicRequest(ir *adapterRequest) ([]byte, error) {
	req := make(map[string]interface{})
	req["model"] = ir.Model
	req["max_tokens"] = 4096 // default if not set
	if ir.MaxTokens != nil {
		req["max_tokens"] = *ir.MaxTokens
	}
	req["stream"] = ir.Stream

	// Add Extra fields
	for k, v := range ir.Extra {
		req[k] = v
	}

	// Add system message from Instructions
	if isAnthropicFamily(ir.SourceFormat) && len(ir.InstructionsRaw) > 0 {
		req["system"] = ir.InstructionsRaw
	} else if ir.Instructions != nil {
		req["system"] = *ir.Instructions
	}

	// Convert messages
	messages := make([]map[string]interface{}, 0)
	for _, msg := range ir.Messages {
		msgMap := make(map[string]interface{})
		role := anthropicRole(msg.Role)
		if role == "tool" || role == "function" {
			role = "user"
		}
		msgMap["role"] = role

		blocks := anthropicBlocksFromMessage(ir.SourceFormat, msg)
		if len(blocks) > 0 {
			msgMap["content"] = blocks
		}

		messages = append(messages, msgMap)
	}
	req["messages"] = messages

	// Generation parameters
	if ir.Temperature != nil {
		req["temperature"] = *ir.Temperature
	}
	if ir.TopP != nil {
		req["top_p"] = *ir.TopP
	}
	if ir.TopK != nil {
		req["top_k"] = *ir.TopK
	}
	if len(ir.StopWords) > 0 {
		req["stop_sequences"] = ir.StopWords
	}
	toolChoice := anthropicToolChoice(ir.ToolChoice, ir.ParallelToolCalls)
	forcedToolChoice := anthropicToolChoiceForcesToolUse(toolChoice)
	if forcedToolChoice {
		removeAnthropicForcedThinkingExtras(req)
	}
	if thinking := anthropicThinkingFromAdapterRequest(ir); thinking != nil && !forcedToolChoice {
		req["thinking"] = thinking
	}
	if ir.Tools != nil {
		if tools := anthropicTools(ir.Tools); len(tools) > 0 {
			req["tools"] = tools
		}
	}
	if toolChoice != nil {
		req["tool_choice"] = toolChoice
	}

	return json.Marshal(req)
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
	if function, ok := choice["function"].(map[string]interface{}); ok {
		if name, ok := function["name"].(string); ok {
			return name
		}
	}
	if name, ok := choice["name"].(string); ok {
		return name
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

func anthropicTools(tools []schema.Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		normalized := anthropicTool(tool)
		if normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func anthropicTool(tool schema.Tool) map[string]interface{} {
	if strings.TrimSpace(tool.Type) == "web_search" {
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
		for key, value := range tool.Extra {
			out[key] = value
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

func anthropicBlocksFromMessage(source Format, msg adapterTurn) []map[string]interface{} {
	items := canonicalMessageParts(msg)
	blocks := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if isAnthropicFamily(source) && len(item.Raw) > 0 {
			var raw map[string]interface{}
			if err := json.Unmarshal(item.Raw, &raw); err == nil {
				blocks = append(blocks, raw)
				continue
			}
		}
		if block := anthropicBlockFromItem(item); block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func anthropicBlockFromItem(item adapterItem) map[string]interface{} {
	switch item.Kind {
	case contentItemKindReasoning:
		return anthropicReasoningBlock(item.Content)
	case contentItemKindContent:
		return anthropicContentBlock(item.Content)
	case contentItemKindToolCall:
		return anthropicToolUseBlock(item.ToolCall)
	case contentItemKindToolResult:
		return anthropicToolResultBlock(item.ToolResult)
	default:
		return nil
	}
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
	registerAdapterRequestParser(FormatAnthropic, parseAnthropicRequest)
	registerAdapterRequestEmitter(FormatAnthropic, emitAnthropicRequest)
}
