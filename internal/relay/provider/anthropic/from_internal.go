package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
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
	if req.TopK != nil && *req.TopK > 0 {
		anthReq["top_k"] = *req.TopK
	}
	if req.Thinking != nil {
		anthReq["thinking"] = req.Thinking
	}
	if req.ParallelToolCalls != nil {
		anthReq["parallel_tool_calls"] = *req.ParallelToolCalls
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
					block := map[string]interface{}{
						"type": "text",
						"text": part.Text,
					}
					// Include Extra (cache_control etc) if present
					if part.Extra != nil {
						for k, v := range part.Extra {
							block[k] = v
						}
					}
					systemParts = append(systemParts, block)
				} else {
					return nil, fmt.Errorf("anthropic system content part type %q cannot be converted", part.Type)
				}
			}
			// Handle reasoning/thinking content in system messages
			for _, rc := range im.ReasoningContent {
				if rc.Type == "thinking" || rc.Type == "reasoning" {
					systemParts = append(systemParts, map[string]interface{}{
						"type":     "thinking",
						"thinking": rc.Text,
					})
				}
			}
		case "tool":
			// Tool result → tool_result content block in user message
			anthropicMsgs = append(anthropicMsgs, buildAnthropicToolResult(im))
		case "assistant":
			msg, err := buildAnthropicAssistantMessage(im)
			if err != nil {
				return nil, err
			}
			anthropicMsgs = append(anthropicMsgs, msg)
		default:
			// user message — convert content format
			msg, err := buildAnthropicUserMessage(im)
			if err != nil {
				return nil, err
			}
			anthropicMsgs = append(anthropicMsgs, msg)
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

	// Merge ExtraParams for same-protocol passthrough
	for k, v := range req.ExtraParams {
		if _, exists := anthReq[k]; !exists {
			anthReq[k] = v
		}
	}

	return json.Marshal(anthReq)
}

// buildAnthropicToolResult converts an InternalMessage with ToolResult to Anthropic format.
func buildAnthropicToolResult(im provider.InternalMessage) map[string]interface{} {
	var contentBlocks []interface{}
	if im.ToolResult != nil {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type": "text",
			"text": im.ToolResult.Content,
		})
	}

	result := map[string]interface{}{
		"type":        "tool_result",
		"tool_use_id": im.ToolResult.ToolCallID,
		"content":     contentBlocks,
	}
	if im.ToolResult.IsError {
		result["is_error"] = true
	}

	return map[string]interface{}{
		"role":    "user",
		"content": []interface{}{result},
	}
}

// buildAnthropicAssistantMessage converts an InternalMessage with assistant role to Anthropic format.
func buildAnthropicAssistantMessage(im provider.InternalMessage) (map[string]interface{}, error) {
	if len(im.ToolCalls) == 0 {
		// Simple text message
		text, err := contentToText(im.Content)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"role":    "assistant",
			"content": text,
		}, nil
	}

	// Assistant with tool_calls → multiple content blocks
	var blocks []interface{}

	// Add text content if present
	text, err := contentToText(im.Content)
	if err != nil {
		return nil, err
	}
	if text != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "text",
			"text": text,
		})
	}

	// Add tool_use blocks
	for _, itc := range im.ToolCalls {
		args := itc.Arguments
		if args == "" || !json.Valid([]byte(args)) {
			if args == "" {
				args = "{}"
			} else {
				return nil, fmt.Errorf("anthropic tool call arguments must be valid JSON")
			}
		}
		blocks = append(blocks, map[string]interface{}{
			"type":  "tool_use",
			"id":    itc.ID,
			"name":  itc.Name,
			"input": json.RawMessage(args),
		})
	}

	return map[string]interface{}{
		"role":    "assistant",
		"content": blocks,
	}, nil
}

// buildAnthropicUserMessage converts an InternalMessage with user role to Anthropic format.
func buildAnthropicUserMessage(im provider.InternalMessage) (map[string]interface{}, error) {
	content, err := buildAnthropicContent(im.Content)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"role":    "user",
		"content": content,
	}, nil
}

// buildAnthropicContent converts InternalContentPart slice to Anthropic content format.
func buildAnthropicContent(parts []provider.InternalContentPart) (interface{}, error) {
	if len(parts) == 0 {
		return "", nil
	}
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].ImageURL == nil {
		return parts[0].Text, nil
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
				if strings.HasPrefix(*part.ImageURL, "data:") {
					mime, data, ok := parseDataURL(*part.ImageURL)
					if ok {
						blocks = append(blocks, map[string]interface{}{
							"type": "image",
							"source": map[string]interface{}{
								"type":       "base64",
								"media_type": mime,
								"data":       data,
							},
						})
						continue
					}
				}
				blocks = append(blocks, map[string]interface{}{
					"type": "image",
					"source": map[string]interface{}{
						"type": "url",
						"url":  *part.ImageURL,
					},
				})
			}
		default:
			return nil, fmt.Errorf("anthropic content part type %q cannot be converted", part.Type)
		}
	}
	if len(blocks) == 0 {
		return "", nil
	}
	return blocks, nil
}

func parseDataURL(url string) (mime string, data string, ok bool) {
	rest := strings.TrimPrefix(url, "data:")
	parts := strings.SplitN(rest, ",", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	mime = strings.TrimSuffix(parts[0], ";base64")
	if mime == "" || parts[1] == "" {
		return "", "", false
	}
	return mime, parts[1], true
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
func contentToText(parts []provider.InternalContentPart) (string, error) {
	var b strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", fmt.Errorf("anthropic content part type %q cannot be converted to plain text", part.Type)
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}
