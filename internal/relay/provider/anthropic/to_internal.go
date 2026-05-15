package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

// anthropicToInternal converts an Anthropic Messages API request body
// into the intermediate InternalRequest format.
func anthropicToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}

	ir := &provider.InternalRequest{
		Metadata: make(map[string]interface{}),
	}

	// Model
	ir.Model, _ = req["model"].(string)

	// Stream
	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}

	// MaxTokens
	if v, ok := req["max_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	}

	// Temperature
	if v, ok := req["temperature"].(float64); ok {
		ir.Temperature = &v
	}

	// TopP
	if v, ok := req["top_p"].(float64); ok {
		ir.TopP = &v
	}

	// StopWords (Anthropic uses "stop_sequences")
	if ss, ok := req["stop_sequences"].([]interface{}); ok {
		for _, item := range ss {
			if str, ok := item.(string); ok {
				ir.StopWords = append(ir.StopWords, str)
			}
		}
	}

	// System → first system message
	// Anthropic puts system at top level as string or array
	var systemContent []provider.InternalContentPart
	if sys, ok := req["system"].([]interface{}); ok {
		for _, part := range sys {
			if m, ok := part.(map[string]interface{}); ok {
				typ, _ := m["type"].(string)
				if typ == "text" {
					text, _ := m["text"].(string)
					systemContent = append(systemContent, provider.InternalContentPart{
						Type: "text",
						Text: text,
					})
				}
			}
		}
	} else if sys, ok := req["system"].(string); ok && sys != "" {
		systemContent = append(systemContent, provider.InternalContentPart{
			Type: "text",
			Text: sys,
		})
	}

	// Messages
	messages, _ := req["messages"].([]interface{})
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
			continue
		}
		im := parseAnthropicMessage(msg)
		ir.Messages = append(ir.Messages, im)
	}

	// Tools
	if tools, ok := req["tools"].([]interface{}); ok {
		ir.Tools = make([]provider.InternalTool, 0, len(tools))
		for _, toolRaw := range tools {
			tool, ok := toolRaw.(map[string]interface{})
			if !ok {
				continue
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
		ir.ToolChoice = parseAnthropicToolChoice(tc)
	}

	return ir, nil
}

// parseAnthropicMessage converts a single Anthropic message to InternalMessage.
func parseAnthropicMessage(msg map[string]interface{}) provider.InternalMessage {
	im := provider.InternalMessage{}
	im.Role, _ = msg["role"].(string)

	// Parse content
	content := msg["content"]
	switch c := content.(type) {
	case string:
		if c != "" {
			im.Content = []provider.InternalContentPart{{Type: "text", Text: c}}
		}
	case []interface{}:
		// Could contain text blocks, tool_use blocks, or tool_result blocks
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)

			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				im.Content = append(im.Content, provider.InternalContentPart{
					Type: "text",
					Text: text,
				})
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				args := "{}"
				if a, err := json.Marshal(block["input"]); err == nil {
					args = string(a)
				}
				im.ToolCalls = append(im.ToolCalls, provider.InternalToolCall{
					ID:        id,
					Name:      name,
					Arguments: args,
				})
			case "tool_result":
				toolUseID, _ := block["tool_use_id"].(string)
				// Extract content from tool_result
				var resultContent string
				if resultParts, ok := block["content"].([]interface{}); ok {
					var parts []string
					for _, rp := range resultParts {
						if rpm, ok := rp.(map[string]interface{}); ok {
							if t, ok := rpm["text"].(string); ok {
								parts = append(parts, t)
							}
						}
					}
					resultContent = joinStrings(parts)
				} else if s, ok := block["content"].(string); ok {
					resultContent = s
				}
				isError := false
				if e, ok := block["is_error"].(bool); ok {
					isError = e
				}
				im.ToolResult = &provider.InternalToolResult{
					Name:       "", // Anthropic tool_result doesn't carry function name; matched by tool_use_id
					ToolCallID: toolUseID,
					Content:    resultContent,
					IsError:    isError,
				}
			case "image":
				// Anthropic image block: {type: "image", source: {type: "url", url: "..."}}
				part := provider.InternalContentPart{Type: "image_url"}
				if source, ok := block["source"].(map[string]interface{}); ok {
					if url, ok := source["url"].(string); ok {
						part.ImageURL = &url
					}
				}
				im.Content = append(im.Content, part)
			}
		}
	}

	return im
}

// parseAnthropicToolChoice converts Anthropic tool_choice to InternalToolChoice.
func parseAnthropicToolChoice(tc interface{}) *provider.InternalToolChoice {
	switch v := tc.(type) {
	case map[string]interface{}:
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
		}
		return itc
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
