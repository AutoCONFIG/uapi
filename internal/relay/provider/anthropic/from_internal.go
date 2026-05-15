package anthropic

import (
	"encoding/json"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

// internalToAnthropic converts InternalRequest to Anthropic Messages API JSON.
func internalToAnthropic(req *provider.InternalRequest) ([]byte, error) {
	anthReq := make(map[string]interface{})
	anthReq["model"] = req.Model
	anthReq["stream"] = req.Stream

	// max_tokens (required in Anthropic)
	maxTokens := 4096
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	anthReq["max_tokens"] = maxTokens

	// Optional params
	if req.Temperature != nil {
		anthReq["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		anthReq["top_p"] = *req.TopP
	}
	if len(req.StopWords) > 0 {
		anthReq["stop_sequences"] = req.StopWords
	}

	// Convert messages: extract system, map roles, handle tool_calls
	var systemParts []interface{}
	var anthropicMsgs []interface{}

	for _, im := range req.Messages {
		switch im.Role {
		case "system":
			// Extract to top-level system field
			for _, part := range im.Content {
				if part.Type == "text" {
					systemParts = append(systemParts, map[string]interface{}{
						"type": "text",
						"text": part.Text,
					})
				}
			}
		case "tool":
			// Tool result → tool_result content block in user message
			anthropicMsgs = append(anthropicMsgs, buildAnthropicToolResult(im))
		case "assistant":
			anthropicMsgs = append(anthropicMsgs, buildAnthropicAssistantMessage(im))
		default:
			// user message — convert content format
			anthropicMsgs = append(anthropicMsgs, buildAnthropicUserMessage(im))
		}
	}

	if len(systemParts) > 0 {
		anthReq["system"] = systemParts
	}
	anthReq["messages"] = anthropicMsgs

	// Convert tools if present
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			anthTool := map[string]interface{}{
				"name":        it.Name,
				"description": it.Description,
			}
			if it.Parameters != nil {
				anthTool["input_schema"] = it.Parameters
			}
			tools = append(tools, anthTool)
		}
		anthReq["tools"] = tools
	}

	// ToolChoice
	if req.ToolChoice != nil {
		anthReq["tool_choice"] = buildAnthropicToolChoice(req.ToolChoice)
	}

	return json.Marshal(anthReq)
}

// buildAnthropicToolResult converts an InternalMessage with ToolResult to Anthropic format.
func buildAnthropicToolResult(im provider.InternalMessage) map[string]interface{} {
	var contentBlocks []interface{}
	if im.ToolResult != nil {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type":       "text",
			"text":       im.ToolResult.Content,
		})
	}

	return map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{
				"type":       "tool_result",
				"tool_use_id": im.ToolResult.ToolCallID,
				"content":    contentBlocks,
			},
		},
	}
}

// buildAnthropicAssistantMessage converts an InternalMessage with assistant role to Anthropic format.
func buildAnthropicAssistantMessage(im provider.InternalMessage) map[string]interface{} {
	if len(im.ToolCalls) == 0 {
		// Simple text message
		text := contentToText(im.Content)
		return map[string]interface{}{
			"role":    "assistant",
			"content": text,
		}
	}

	// Assistant with tool_calls → multiple content blocks
	var blocks []interface{}

	// Add text content if present
	if text := contentToText(im.Content); text != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "text",
			"text": text,
		})
	}

	// Add tool_use blocks
	for _, itc := range im.ToolCalls {
		args := json.RawMessage(itc.Arguments)
		blocks = append(blocks, map[string]interface{}{
			"type":  "tool_use",
			"id":    itc.ID,
			"name":  itc.Name,
			"input": args,
		})
	}

	return map[string]interface{}{
		"role":    "assistant",
		"content": blocks,
	}
}

// buildAnthropicUserMessage converts an InternalMessage with user role to Anthropic format.
func buildAnthropicUserMessage(im provider.InternalMessage) map[string]interface{} {
	content := buildAnthropicContent(im.Content)
	return map[string]interface{}{
		"role":    "user",
		"content": content,
	}
}

// buildAnthropicContent converts InternalContentPart slice to Anthropic content format.
func buildAnthropicContent(parts []provider.InternalContentPart) interface{} {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].ImageURL == nil {
		return parts[0].Text
	}

	var blocks []interface{}
	for _, part := range parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": part.Text,
			})
		case "image_url":
			if part.ImageURL != nil {
				blocks = append(blocks, map[string]interface{}{
					"type": "image",
					"source": map[string]interface{}{
						"type": "url",
						"url":  *part.ImageURL,
					},
				})
			}
		default:
			blocks = append(blocks, map[string]interface{}{
				"type": part.Type,
				"text": part.Text,
			})
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return blocks
}

// buildAnthropicToolChoice converts InternalToolChoice to Anthropic tool_choice format.
func buildAnthropicToolChoice(tc *provider.InternalToolChoice) interface{} {
	switch tc.Type {
	case "auto":
		return map[string]interface{}{"type": "auto"}
	case "none":
		return map[string]interface{}{"type": "none"}
	case "required":
		return map[string]interface{}{"type": "any"}
	case "function":
		return map[string]interface{}{
			"type": "tool",
			"name": tc.Function,
		}
	default:
		return map[string]interface{}{"type": "auto"}
	}
}

// contentToText extracts plain text from content parts.
func contentToText(parts []provider.InternalContentPart) string {
	for _, part := range parts {
		if part.Type == "text" {
			return part.Text
		}
	}
	return ""
}
