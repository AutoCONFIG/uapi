package openai

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
)

// openaiResponseToInternal converts an OpenAI Chat Completions response body
// into the intermediate InternalResponse format.
func openaiResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse openai response: %w", err)
	}

	ir := &provider.InternalResponse{}
	ir.ID, _ = resp["id"].(string)
	ir.Model, _ = resp["model"].(string)

	// Parse choices
	choices, _ := resp["choices"].([]interface{})
	ir.Choices = make([]provider.InternalChoice, 0, len(choices))
	for _, chRaw := range choices {
		ch, ok := chRaw.(map[string]interface{})
		if !ok {
			continue
		}
		ic := provider.InternalChoice{}

		// Index
		if idx, ok := ch["index"].(float64); ok {
			ic.Index = int(idx)
		}

		// Message
		if msg, ok := ch["message"].(map[string]interface{}); ok {
			im := provider.InternalMessage{}
			im.Role, _ = msg["role"].(string)

			// Content: can be a string or an array of content parts
			im.Content = parseResponseContent(msg["content"])

			// Tool calls
			if tcs, ok := msg["tool_calls"].([]interface{}); ok {
				im.ToolCalls = make([]provider.InternalToolCall, 0, len(tcs))
				for _, tcRaw := range tcs {
					tc, ok := tcRaw.(map[string]interface{})
					if !ok {
						continue
					}
					itc := provider.InternalToolCall{
						ID: intfStr(tc["id"]),
					}
					if fn, ok := tc["function"].(map[string]interface{}); ok {
						itc.Name, _ = fn["name"].(string)
						itc.Arguments, _ = fn["arguments"].(string)
					}
					im.ToolCalls = append(im.ToolCalls, itc)
				}
			}

			ic.Message = im
		}

		// Finish reason
		ic.FinishReason, _ = ch["finish_reason"].(string)

		ir.Choices = append(ir.Choices, ic)
	}

	// Usage
	if usage, ok := resp["usage"].(map[string]interface{}); ok {
		ir.Usage = provider.InternalUsage{}
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			ir.Usage.PromptTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			ir.Usage.CompletionTokens = int(ct)
		}
	}

	return ir, nil
}

// parseResponseContent parses OpenAI response content into InternalContentPart slice.
// Content can be a string or an array of content part objects.
func parseResponseContent(content interface{}) []provider.InternalContentPart {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []provider.InternalContentPart{{Type: "text", Text: c}}
	case []interface{}:
		var parts []provider.InternalContentPart
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				part := provider.InternalContentPart{}
				part.Type, _ = m["type"].(string)
				part.Text, _ = m["text"].(string)
				if imgURL, ok := m["image_url"].(map[string]interface{}); ok {
					url, _ := imgURL["url"].(string)
					part.ImageURL = &url
				}
				parts = append(parts, part)
			}
		}
		return parts
	default:
		return nil
	}
}

// internalToOpenAIResponse converts InternalResponse into OpenAI Chat Completions JSON.
func internalToOpenAIResponse(resp *provider.InternalResponse) ([]byte, error) {
	oai := map[string]interface{}{
		"object":  "chat.completion",
		"created": 0, // will be set by caller or left as 0
	}

	oai["id"] = resp.ID
	oai["model"] = resp.Model

	// Choices
	choices := make([]interface{}, 0, len(resp.Choices))
	for _, ch := range resp.Choices {
		msg := map[string]interface{}{
			"role": ch.Message.Role,
		}

		// Content: if tool_calls exist and content is empty, set content to null
		if len(ch.Message.ToolCalls) > 0 && len(ch.Message.Content) == 0 {
			msg["content"] = nil
		} else if len(ch.Message.Content) > 0 {
			// Single text part can be serialized as a plain string
			if len(ch.Message.Content) == 1 && ch.Message.Content[0].Type == "text" && ch.Message.Content[0].ImageURL == nil {
				msg["content"] = ch.Message.Content[0].Text
			} else {
				content := make([]interface{}, 0, len(ch.Message.Content))
				for _, part := range ch.Message.Content {
					p := map[string]interface{}{"type": part.Type}
					switch part.Type {
					case "text":
						p["text"] = part.Text
					case "image_url":
						if part.ImageURL != nil {
							p["image_url"] = map[string]interface{}{"url": *part.ImageURL}
						}
					}
					content = append(content, p)
				}
				msg["content"] = content
			}
		}

		// Tool calls
		if len(ch.Message.ToolCalls) > 0 {
			tcs := make([]interface{}, 0, len(ch.Message.ToolCalls))
			for i, itc := range ch.Message.ToolCalls {
				tcs = append(tcs, map[string]interface{}{
					"index": i,
					"id":    itc.ID,
					"type":  "function",
					"function": map[string]interface{}{
						"name":      itc.Name,
						"arguments": itc.Arguments,
					},
				})
			}
			msg["tool_calls"] = tcs
		}

		choice := map[string]interface{}{
			"index":         ch.Index,
			"message":       msg,
			"finish_reason": ch.FinishReason,
		}
		choices = append(choices, choice)
	}
	oai["choices"] = choices

	// Usage
	usage := map[string]interface{}{
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"total_tokens":      resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
	}
	oai["usage"] = usage

	return json.Marshal(oai)
}
