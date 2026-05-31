package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// ParseAnthropicRequest converts Anthropic Messages API request to an adapter request.
func ParseAnthropicRequest(body []byte) (*adapterRequest, error) {
	var req schema.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}

	ir := &adapterRequest{
		Model:        req.Model,
		Stream:       req.Stream,
		SourceFormat: FormatAnthropic,
		Extra:        make(map[string]json.RawMessage),
		MaxTokens:    &req.MaxTokens, // Anthropic requires max_tokens
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
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
					dataURI := fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
					part := schema.ContentPart{
						Type:     "image_url",
						ImageURL: &dataURI,
						MimeType: block.Source.MediaType,
						Extra:    block.Extra,
					}
					appendContentItem(&requestMsg, part, rawBlock)
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
					Content:    block.ContentStr,
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

// EmitAnthropicRequest converts an adapter request to Anthropic Messages API request.
func EmitAnthropicRequest(ir *adapterRequest) ([]byte, error) {
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
	if thinking := anthropicThinkingFromAdapterRequest(ir); thinking != nil {
		req["thinking"] = thinking
	}
	if ir.Tools != nil {
		tools, _ := json.Marshal(ir.Tools)
		req["tools"] = json.RawMessage(tools)
	}
	if ir.ToolChoice != nil {
		tc, _ := json.Marshal(ir.ToolChoice)
		req["tool_choice"] = json.RawMessage(tc)
	}

	return json.Marshal(req)
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

func init() {
	RegisterRequestParser(FormatAnthropic, ParseAnthropicRequest)
	RegisterRequestEmitter(FormatAnthropic, EmitAnthropicRequest)
}
