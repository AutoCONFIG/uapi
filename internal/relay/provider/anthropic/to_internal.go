package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// anthropicToInternal converts an Anthropic Messages API request body
// into the intermediate InternalRequest format.
func anthropicToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &req); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}

	ir := &provider.InternalRequest{
		Metadata: make(map[string]interface{}),
	}
	if err := validateAnthropicRequestFieldsConvertible(req); err != nil {
		return nil, err
	}

	// Model
	ir.Model, _ = req["model"].(string)

	// Stream
	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}

	// MaxTokens
	if v, ok := provider.ToFloat64(req["max_tokens"]); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	}

	// Temperature
	if v, ok := provider.ToFloat64(req["temperature"]); ok {
		ir.Temperature = &v
	}

	// TopP
	if v, ok := provider.ToFloat64(req["top_p"]); ok {
		ir.TopP = &v
	}

	// TopK
	if v, ok := req["top_k"]; ok {
		if topK := provider.ToInt(v); topK > 0 {
			ir.TopK = &topK
		}
	}

	// Thinking (Anthropic extended thinking)
	if v, ok := req["thinking"]; ok {
		ir.Thinking = v
	}

	// StopWords (Anthropic uses "stop_sequences")
	if ssRaw, exists := req["stop_sequences"]; exists {
		ss, ok := ssRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("anthropic stop_sequences must be an array")
		}
		for _, item := range ss {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("anthropic stop_sequences entries must be strings")
			}
			ir.StopWords = append(ir.StopWords, str)
		}
	}

	// System → first system message
	// Anthropic puts system at top level as string or array
	var systemContent []provider.InternalContentPart
	if sysRaw, exists := req["system"]; exists {
		switch sys := sysRaw.(type) {
		case []interface{}:
			for _, part := range sys {
				m, ok := part.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("anthropic system content blocks must be objects")
				}
				if _, ok := m["cache_control"]; ok {
					return nil, fmt.Errorf("anthropic system cache_control cannot be converted to non-anthropic upstream formats")
				}
				typ, _ := m["type"].(string)
				if typ != "text" {
					return nil, fmt.Errorf("anthropic system content block type %q cannot be converted to non-anthropic upstream formats", typ)
				}
				if err := validateAllowedAnthropicKeys(m, "anthropic system text block", "type", "text"); err != nil {
					return nil, err
				}
				text, ok := m["text"].(string)
				if !ok {
					return nil, fmt.Errorf("anthropic system text block requires text")
				}
				systemContent = append(systemContent, provider.InternalContentPart{
					Type: "text",
					Text: text,
				})
			}
		case string:
			if sys != "" {
				systemContent = append(systemContent, provider.InternalContentPart{
					Type: "text",
					Text: sys,
				})
			}
		default:
			return nil, fmt.Errorf("anthropic system must be a string or text block array")
		}
	}

	// Messages
	messages, ok := req["messages"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("anthropic messages must be an array")
	}
	ir.Messages = make([]provider.InternalMessage, 0, len(messages)+1)

	// Add system message first if present
	if len(systemContent) > 0 {
		ir.Messages = append(ir.Messages, provider.InternalMessage{
			Role:    "system",
			Content: systemContent,
		})
	}

	for _, msgRaw := range messages {
		msg, ok := msgRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("anthropic messages entries must be objects")
		}
		im, err := parseAnthropicMessage(msg)
		if err != nil {
			return nil, err
		}
		ir.Messages = append(ir.Messages, im)
	}

	// Tools
	if toolsRaw, exists := req["tools"]; exists {
		tools, ok := toolsRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("anthropic tools must be an array")
		}
		ir.Tools = make([]provider.InternalTool, 0, len(tools))
		for _, toolRaw := range tools {
			tool, ok := toolRaw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("anthropic tools entries must be objects")
			}
			if _, ok := tool["cache_control"]; ok {
				return nil, fmt.Errorf("anthropic tool cache_control cannot be converted to non-anthropic upstream formats")
			}
			if typ, _ := tool["type"].(string); typ != "" && typ != "custom" {
				return nil, fmt.Errorf("anthropic tool type %q cannot be converted to non-anthropic upstream formats", typ)
			}
			if err := validateAnthropicToolKeys(tool); err != nil {
				return nil, err
			}
			it := provider.InternalTool{Type: "function"}
			it.Name, _ = tool["name"].(string)
			it.Description, _ = tool["description"].(string)
			it.Parameters = tool["input_schema"]
			ir.Tools = append(ir.Tools, it)
		}
	}

	// ToolChoice
	if tc, ok := req["tool_choice"]; ok {
		choice, err := parseAnthropicToolChoice(tc)
		if err != nil {
			return nil, err
		}
		ir.ToolChoice = choice
	}

	return ir, nil
}

// parseAnthropicMessage converts a single Anthropic message to InternalMessage.
func parseAnthropicMessage(msg map[string]interface{}) (provider.InternalMessage, error) {
	im := provider.InternalMessage{}
	im.Role, _ = msg["role"].(string)
	if err := validateAllowedAnthropicKeys(msg, "anthropic message", "role", "content"); err != nil {
		return im, err
	}

	// Parse content
	content := msg["content"]
	switch c := content.(type) {
	case string:
		if c != "" {
			im.Content = []provider.InternalContentPart{{Type: "text", Text: c}}
		}
	case []interface{}:
		// Could contain text blocks, tool_use blocks, or tool_result blocks
		toolResultSeen := false
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				return im, fmt.Errorf("anthropic content blocks must be objects")
			}
			blockType, _ := block["type"].(string)
			if _, ok := block["cache_control"]; ok {
				return im, fmt.Errorf("anthropic content block cache_control cannot be converted to non-anthropic upstream formats")
			}
			if err := validateAnthropicContentBlockKeys(block); err != nil {
				return im, err
			}

			switch blockType {
			case "text":
				text, ok := block["text"].(string)
				if !ok {
					return im, fmt.Errorf("anthropic text block requires text")
				}
				im.Content = append(im.Content, provider.InternalContentPart{
					Type: "text",
					Text: text,
				})
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				if id == "" || name == "" {
					return im, fmt.Errorf("anthropic tool_use requires id and name")
				}
				args := "{}"
				if inputVal, exists := block["input"]; exists {
					a, err := json.Marshal(inputVal)
					if err != nil {
						return im, fmt.Errorf("anthropic tool_use input must be JSON-serializable: %w", err)
					}
					args = string(a)
				}
				im.ToolCalls = append(im.ToolCalls, provider.InternalToolCall{
					ID:        id,
					Name:      name,
					Arguments: args,
				})
			case "tool_result":
				if toolResultSeen {
					return im, fmt.Errorf("anthropic multiple tool_result blocks in one message cannot be converted to the internal format")
				}
				toolResultSeen = true
				toolUseID, _ := block["tool_use_id"].(string)
				if toolUseID == "" {
					return im, fmt.Errorf("anthropic tool_result requires tool_use_id")
				}
				// Extract content from tool_result
				var resultContent string
				if resultParts, ok := block["content"].([]interface{}); ok {
					var parts []string
					for _, rp := range resultParts {
						rpm, ok := rp.(map[string]interface{})
						if !ok {
							return im, fmt.Errorf("anthropic tool_result content entries must be objects")
						}
						if err := validateAnthropicTextResultPartKeys(rpm); err != nil {
							return im, err
						}
						if typ, _ := rpm["type"].(string); typ != "" && typ != "text" {
							return im, fmt.Errorf("anthropic tool_result content type %q cannot be converted to non-anthropic upstream formats", typ)
						}
						t, ok := rpm["text"].(string)
						if !ok {
							return im, fmt.Errorf("anthropic tool_result text content requires text")
						}
						parts = append(parts, t)
					}
					resultContent = joinStrings(parts)
				} else if s, ok := block["content"].(string); ok {
					resultContent = s
				} else if _, exists := block["content"]; exists {
					return im, fmt.Errorf("anthropic tool_result content must be a string or text block array")
				}
				isError := false
				if e, ok := block["is_error"].(bool); ok {
					isError = e
				} else if _, exists := block["is_error"]; exists {
					return im, fmt.Errorf("anthropic tool_result is_error must be a boolean")
				}
				im.ToolResult = &provider.InternalToolResult{
					Name:       "", // Anthropic tool_result doesn't carry function name; matched by tool_use_id
					ToolCallID: toolUseID,
					Content:    resultContent,
					IsError:    isError,
				}
				im.Role = "tool"
			case "image":
				part := provider.InternalContentPart{Type: "image_url"}
				if source, ok := block["source"].(map[string]interface{}); ok {
					sourceType, _ := source["type"].(string)
					if sourceType == "base64" {
						if err := validateAllowedAnthropicKeys(source, "anthropic image base64 source", "type", "media_type", "data"); err != nil {
							return im, err
						}
						mime, _ := source["media_type"].(string)
						data, _ := source["data"].(string)
						if mime == "" || data == "" {
							return im, fmt.Errorf("anthropic image base64 source is missing media_type or data")
						}
						url := "data:" + mime + ";base64," + data
						part.ImageURL = &url
					} else if sourceType == "url" {
						if err := validateAllowedAnthropicKeys(source, "anthropic image url source", "type", "url"); err != nil {
							return im, err
						}
						url, _ := source["url"].(string)
						if url == "" {
							return im, fmt.Errorf("anthropic image url source is missing url")
						}
						part.ImageURL = &url
					} else {
						return im, fmt.Errorf("anthropic image source type %q cannot be converted to non-anthropic upstream formats", sourceType)
					}
				} else {
					return im, fmt.Errorf("anthropic image block is missing source")
				}
				im.Content = append(im.Content, part)
			default:
				return im, fmt.Errorf("anthropic content block type %q cannot be converted to non-anthropic upstream formats", blockType)
			}
		}
	default:
		return im, fmt.Errorf("anthropic message content must be a string or content block array")
	}

	return im, nil
}

// parseAnthropicToolChoice converts Anthropic tool_choice to InternalToolChoice.
func parseAnthropicToolChoice(tc interface{}) (*provider.InternalToolChoice, error) {
	switch v := tc.(type) {
	case map[string]interface{}:
		if err := validateAllowedAnthropicKeys(v, "anthropic tool_choice", "type", "name"); err != nil {
			return nil, err
		}
		typ, _ := v["type"].(string)
		itc := &provider.InternalToolChoice{Type: typ}
		switch typ {
		case "auto":
			itc.Type = "auto"
		case "none":
			itc.Type = "none"
		case "any":
			itc.Type = "required"
		case "tool":
			itc.Type = "function"
			itc.Function, _ = v["name"].(string)
			if itc.Function == "" {
				return nil, fmt.Errorf("anthropic tool_choice type tool requires name")
			}
		default:
			return nil, fmt.Errorf("anthropic tool_choice type %q cannot be converted to non-anthropic upstream formats", typ)
		}
		return itc, nil
	case nil:
		return &provider.InternalToolChoice{Type: "auto"}, nil
	default:
		return nil, fmt.Errorf("anthropic tool_choice must be an object")
	}
}

func validateAnthropicRequestFieldsConvertible(req map[string]interface{}) error {
	allowed := map[string]struct{}{
		"model":          {},
		"messages":       {},
		"system":         {},
		"max_tokens":     {},
		"temperature":    {},
		"top_p":          {},
		"top_k":          {},
		"stop_sequences": {},
		"stream":         {},
		"tools":          {},
		"tool_choice":    {},
		"thinking":       {},
	}
	for key := range req {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("anthropic request field %q cannot be converted to non-anthropic upstream formats", key)
		}
	}
	return nil
}

func validateAnthropicContentBlockKeys(block map[string]interface{}) error {
	blockType, _ := block["type"].(string)
	allowedByType := map[string]map[string]struct{}{
		"text": {
			"type": {},
			"text": {},
		},
		"tool_use": {
			"type":  {},
			"id":    {},
			"name":  {},
			"input": {},
		},
		"tool_result": {
			"type":        {},
			"tool_use_id": {},
			"content":     {},
			"is_error":    {},
		},
		"image": {
			"type":   {},
			"source": {},
		},
	}
	allowed, ok := allowedByType[blockType]
	if !ok {
		return nil
	}
	for key := range block {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("anthropic content block field %q cannot be converted to non-anthropic upstream formats", key)
		}
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
			return fmt.Errorf("%s field %q cannot be converted to non-anthropic upstream formats", label, key)
		}
	}
	return nil
}

func validateAnthropicToolKeys(tool map[string]interface{}) error {
	return validateAllowedAnthropicKeys(tool, "anthropic tool", "type", "name", "description", "input_schema")
}

func validateAnthropicTextResultPartKeys(part map[string]interface{}) error {
	return validateAllowedAnthropicKeys(part, "anthropic tool_result content", "type", "text")
}

func validateAllowedAnthropicKeys(m map[string]interface{}, label string, keys ...string) error {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for key := range m {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q cannot be converted to non-anthropic upstream formats", label, key)
		}
	}
	return nil
}

func joinStrings(parts []string) string {
	return strings.Join(parts, "\n")
}
