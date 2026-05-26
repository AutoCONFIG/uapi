package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// openaiResponseToInternal converts an OpenAI Chat Completions API response body
// into the intermediate InternalResponse format.
func openaiResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse openai response: %w", err)
	}

	ir := &provider.InternalResponse{Metadata: make(map[string]interface{})}
	ir.ID, _ = resp["id"].(string)
	ir.Model, _ = resp["model"].(string)
	if createdAt := provider.ToInt(resp["created_at"]); createdAt > 0 {
		ir.Metadata["openai_responses_created_at"] = createdAt
	} else if createdAt := provider.ToInt(resp["created"]); createdAt > 0 {
		ir.Metadata["openai_responses_created_at"] = createdAt
	}

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
			content, err := parseResponseContent(msg["content"])
			if err != nil {
				return nil, err
			}
			im.Content = content
			if reasoning := openAIReasoningText(msg); reasoning != "" {
				im.ReasoningContent = append(im.ReasoningContent, provider.InternalContentPart{Type: "reasoning", Text: reasoning})
			}
			// Handle refusal: store in Refusal field instead of hard error
			if refusal, ok := msg["refusal"].(string); ok && refusal != "" && len(im.Content) == 0 {
				im.Content = []provider.InternalContentPart{{Type: "refusal", Text: refusal, Refusal: refusal}}
			}
			// Handle audio: store in Metadata instead of hard error
			if audio, ok := msg["audio"]; ok && audio != nil {
				if ir.Metadata == nil {
					ir.Metadata = make(map[string]interface{})
				}
				ir.Metadata["openai_chat_audio"] = audio
			}

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
		// Extract cache tokens from prompt_tokens_details
		if ptd, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
			ir.Usage.PromptTokensDetails = ptd
			if cached, ok := ptd["cached_tokens"].(float64); ok {
				ir.Usage.CacheReadInputTokens = int(cached)
			}
		}
		// Extract cache_creation_input_tokens directly if present
		if cc, ok := usage["cache_creation_input_tokens"].(float64); ok {
			ir.Usage.CacheCreationInputTokens = int(cc)
		}
		if cr, ok := usage["cache_read_input_tokens"].(float64); ok {
			ir.Usage.CacheReadInputTokens = int(cr)
		}
		// Extract completion_tokens_details if present
		if ctd, ok := usage["completion_tokens_details"].(map[string]interface{}); ok {
			ir.Usage.CompletionTokensDetails = ctd
		}
	}

	return ir, nil
}

func openAIReasoningText(msg map[string]interface{}) string {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if reasoning, ok := msg[key].(string); ok && reasoning != "" {
			return reasoning
		}
	}
	return ""
}

// parseResponseContent parses OpenAI response content into InternalContentPart slice.
// Content can be a string or an array of content part objects.
func parseResponseContent(content interface{}) ([]provider.InternalContentPart, error) {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil, nil
		}
		return []provider.InternalContentPart{{Type: "text", Text: c}}, nil
	case []interface{}:
		var parts []provider.InternalContentPart
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				typ, _ := m["type"].(string)
				part := provider.InternalContentPart{}
				part.Type = typ
				part.Text, _ = m["text"].(string)
				switch typ {
				case "text":
					if part.Text == "" {
						return nil, fmt.Errorf("openai chat response text content part requires text")
					}
				case "refusal":
					// Handle refusal content part type - store in Refusal field
					part.Refusal, _ = m["refusal"].(string)
					if part.Text == "" {
						part.Text = part.Refusal
					}
				case "image_url":
				case "reasoning":
					// Handle reasoning content part type
					part.Text, _ = m["reasoning"].(string)
				case "audio":
					// Handle audio content part type - store in part's Extra for metadata
					if part.Extra == nil {
						part.Extra = make(map[string]interface{})
					}
					if audioData, ok := m["audio"]; ok {
						part.Extra["audio"] = audioData
					}
				default:
					// Allow unknown types to pass through without error
				}
				if imgURL, ok := m["image_url"].(map[string]interface{}); ok {
					url, _ := imgURL["url"].(string)
					part.ImageURL = &url
					if typ == "image_url" && url == "" {
						return nil, fmt.Errorf("openai chat response image_url content part requires image_url.url")
					}
				} else if typ == "image_url" {
					return nil, fmt.Errorf("openai chat response image_url content part requires image_url object")
				}
				parts = append(parts, part)
			}
		}
		return parts, nil
	default:
		return nil, nil
	}
}

func responsesResponseToInternal(body []byte) (*provider.InternalResponse, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse openai responses response: %w", err)
	}
	ir := &provider.InternalResponse{
		Metadata: map[string]interface{}{
			"openai_responses_raw": resp,
		},
	}
	ir.ID, _ = resp["id"].(string)
	ir.Model, _ = resp["model"].(string)
	if createdAt := provider.ToInt(resp["created_at"]); createdAt > 0 {
		ir.Metadata["openai_responses_created_at"] = createdAt
	}
	status, _ := resp["status"].(string)
	if status == "failed" {
		return nil, fmt.Errorf("openai responses status %q cannot be converted to successful chat response", status)
	}
	if errObj, ok := resp["error"].(map[string]interface{}); ok && errObj != nil {
		return nil, fmt.Errorf("openai responses error cannot be converted to successful chat response")
	}
	choice := provider.InternalChoice{
		Index: 0,
		Message: provider.InternalMessage{
			Role: "assistant",
		},
		FinishReason: "stop",
	}
	if status == "incomplete" {
		choice.FinishReason = responsesIncompleteFinishReason(resp)
	}
	if output, ok := resp["output"].([]interface{}); ok {
		for _, raw := range output {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			itemType, _ := item["type"].(string)
			switch itemType {
			case "message":
				if role, _ := item["role"].(string); role != "" {
					choice.Message.Role = role
				}
				if content, ok := item["content"].([]interface{}); ok {
					for _, cRaw := range content {
						c, ok := cRaw.(map[string]interface{})
						if !ok {
							continue
						}
						switch typ, _ := c["type"].(string); typ {
						case "output_text", "text":
							if annotations, ok := c["annotations"].([]interface{}); ok && len(annotations) > 0 {
								warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
							}
							if text, _ := c["text"].(string); text != "" {
								choice.Message.Content = append(choice.Message.Content, provider.InternalContentPart{Type: "text", Text: text})
							}
						case "refusal":
							refusalText, _ := c["refusal"].(string)
							choice.Message.Content = append(choice.Message.Content, provider.InternalContentPart{Type: "refusal", Text: refusalText, Refusal: refusalText})
						default:
							// Allow unknown content types to pass through
						}
					}
				}
			case "reasoning":
				// Handle reasoning output item - map to ReasoningContent
				reasoningText, _ := item["summary"].([]interface{})
				var text string
				if len(reasoningText) > 0 {
					if s, ok := reasoningText[0].(string); ok {
						text = s
					}
				}
				if text == "" {
					text, _ = item["content"].(string)
				}
				if text != "" {
					choice.Message.ReasoningContent = append(choice.Message.ReasoningContent, provider.InternalContentPart{Type: "reasoning", Text: text})
				}
			case "function_call":
				id, _ := item["call_id"].(string)
				if id == "" {
					id, _ = item["id"].(string)
				}
				name, _ := item["name"].(string)
				args, _ := item["arguments"].(string)
				choice.Message.ToolCalls = append(choice.Message.ToolCalls, provider.InternalToolCall{ID: id, Name: name, Arguments: args})
				if choice.FinishReason == "stop" {
					choice.FinishReason = "tool_calls"
				}
			default:
				if itemType != "" {
					warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{itemType})
				}
			}
		}
	}
	ir.Choices = []provider.InternalChoice{choice}
	if usage, ok := resp["usage"].(map[string]interface{}); ok {
		ir.Usage.PromptTokens = provider.ToInt(usage["input_tokens"])
		ir.Usage.CompletionTokens = provider.ToInt(usage["output_tokens"])
		// Extract cache tokens from prompt_tokens_details
		if ptd, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
			ir.Usage.PromptTokensDetails = ptd
			if cached, ok := ptd["cached_tokens"].(float64); ok {
				ir.Usage.CacheReadInputTokens = int(cached)
			}
		}
		// Extract cache_creation_input_tokens directly if present
		if cc, ok := usage["cache_creation_input_tokens"].(float64); ok {
			ir.Usage.CacheCreationInputTokens = int(cc)
		}
		if cr, ok := usage["cache_read_input_tokens"].(float64); ok {
			ir.Usage.CacheReadInputTokens = int(cr)
		}
		// Extract completion_tokens_details if present
		if ctd, ok := usage["completion_tokens_details"].(map[string]interface{}); ok {
			ir.Usage.CompletionTokensDetails = ctd
		}
	}
	return ir, nil
}

// internalToOpenAIResponse converts InternalResponse into OpenAI Chat Completions API JSON.
func internalToOpenAIResponse(resp *provider.InternalResponse) ([]byte, error) {
	oai := map[string]interface{}{
		"object": "chat.completion",
	}
	if createdAt, ok := resp.Metadata["openai_responses_created_at"].(int); ok && createdAt > 0 {
		oai["created"] = createdAt
	} else {
		oai["created"] = time.Now().Unix()
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
							img := map[string]interface{}{"url": *part.ImageURL}
							if part.ImageDetail != "" {
								img["detail"] = part.ImageDetail
							}
							p["image_url"] = img
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
			for _, itc := range ch.Message.ToolCalls {
				tcs = append(tcs, map[string]interface{}{
					"id":   itc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      itc.Name,
						"arguments": itc.Arguments,
					},
				})
			}
			msg["tool_calls"] = tcs
		}
		if reasoning := internalReasoningText(ch.Message.ReasoningContent); reasoning != "" {
			msg["reasoning_content"] = reasoning
			msg["reasoning"] = reasoning
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

func internalReasoningText(parts []provider.InternalContentPart) string {
	out := ""
	for _, part := range parts {
		if part.Text != "" {
			out += part.Text
		}
	}
	return out
}

func internalToResponsesResponse(resp *provider.InternalResponse) ([]byte, error) {
	if resp.Metadata != nil {
		if raw, ok := resp.Metadata["openai_responses_raw"].(map[string]interface{}); ok {
			return json.Marshal(raw)
		}
	}
	output := make([]interface{}, 0, len(resp.Choices))
	status := "completed"
	var incompleteReason string
	for _, ch := range resp.Choices {
		if responsesFinishReasonIncomplete(ch.FinishReason) {
			status = "incomplete"
			incompleteReason = responsesIncompleteReason(ch.FinishReason)
		}
		content := make([]interface{}, 0, len(ch.Message.Content))
		for _, part := range ch.Message.Content {
			switch part.Type {
			case "text":
				content = append(content, map[string]interface{}{
					"type":        "output_text",
					"text":        part.Text,
					"annotations": []interface{}{},
				})
			case "image_url":
				if part.ImageURL != nil {
					item := map[string]interface{}{
						"type":      "output_image",
						"image_url": *part.ImageURL,
					}
					if part.ImageDetail != "" {
						item["detail"] = part.ImageDetail
					}
					content = append(content, item)
				}
			}
		}
		if len(content) > 0 || len(ch.Message.ToolCalls) == 0 {
			output = append(output, map[string]interface{}{
				"id":      "msg_" + provider.RandomHex(8),
				"type":    "message",
				"status":  status,
				"role":    "assistant",
				"content": content,
			})
		}
		for _, tc := range ch.Message.ToolCalls {
			output = append(output, map[string]interface{}{
				"id":        "fc_" + provider.RandomHex(8),
				"type":      "function_call",
				"status":    status,
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": tc.Arguments,
			})
		}
	}
	createdAt := time.Now().Unix()
	if resp.Metadata != nil {
		if v, ok := resp.Metadata["openai_responses_created_at"].(int); ok && v > 0 {
			createdAt = int64(v)
		}
	}
	result := map[string]interface{}{
		"id":         resp.ID,
		"object":     "response",
		"created_at": createdAt,
		"model":      resp.Model,
		"output":     output,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
		},
	}
	if len(resp.Choices) > 0 {
		result["status"] = status
	}
	if status == "incomplete" {
		result["incomplete_details"] = map[string]interface{}{"reason": incompleteReason}
	}
	return json.Marshal(result)
}
