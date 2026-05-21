package openai

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// openaiChatToInternal converts an OpenAI Chat Completions request body
// into the intermediate InternalRequest format.
func openaiChatToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse openai chat request: %w", err)
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

	// StopWords
	switch s := req["stop"].(type) {
	case string:
		if s != "" {
			ir.StopWords = []string{s}
		}
	case []interface{}:
		for _, item := range s {
			if str, ok := item.(string); ok {
				ir.StopWords = append(ir.StopWords, str)
			}
		}
	}

	// Messages
	messages, _ := req["messages"].([]interface{})
	ir.Messages = make([]provider.InternalMessage, 0, len(messages))
	for _, msgRaw := range messages {
		msg, ok := msgRaw.(map[string]interface{})
		if !ok {
			continue
		}
		im := parseOpenAIMessage(msg)
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
			if fn, ok := tool["function"].(map[string]interface{}); ok {
				it.Name, _ = fn["name"].(string)
				it.Description, _ = fn["description"].(string)
				it.Parameters = fn["parameters"]
			}
			ir.Tools = append(ir.Tools, it)
		}
	}

	// ToolChoice
	if tc, ok := req["tool_choice"]; ok {
		ir.ToolChoice = parseOpenAIToolChoice(tc)
	}

	return ir, nil
}

func responsesToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse openai responses request: %w", err)
	}
	ir := &provider.InternalRequest{Metadata: make(map[string]interface{})}
	ir.Model, _ = req["model"].(string)
	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}
	if v, ok := req["max_output_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	} else if v, ok := req["max_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	}
	if v, ok := req["temperature"].(float64); ok {
		ir.Temperature = &v
	}
	if v, ok := req["top_p"].(float64); ok {
		ir.TopP = &v
	}
	if instructions, ok := req["instructions"].(string); ok && instructions != "" {
		ir.Messages = append(ir.Messages, provider.InternalMessage{
			Role:    "system",
			Content: []provider.InternalContentPart{{Type: "text", Text: instructions}},
		})
	}
	ir.Messages = append(ir.Messages, parseResponsesInput(req["input"])...)
	return ir, nil
}

func parseResponsesInput(input interface{}) []provider.InternalMessage {
	switch v := input.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []provider.InternalMessage{{Role: "user", Content: []provider.InternalContentPart{{Type: "text", Text: v}}}}
	case []interface{}:
		out := make([]provider.InternalMessage, 0, len(v))
		for _, item := range v {
			switch msg := item.(type) {
			case string:
				if msg != "" {
					out = append(out, provider.InternalMessage{Role: "user", Content: []provider.InternalContentPart{{Type: "text", Text: msg}}})
				}
			case map[string]interface{}:
				out = append(out, parseResponsesMessage(msg))
			}
		}
		return out
	default:
		return nil
	}
}

func parseResponsesMessage(msg map[string]interface{}) provider.InternalMessage {
	role, _ := msg["role"].(string)
	if role == "" {
		role = "user"
	}
	return provider.InternalMessage{
		Role:    role,
		Content: parseResponsesContent(msg["content"]),
	}
}

func parseResponsesContent(content interface{}) []provider.InternalContentPart {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []provider.InternalContentPart{{Type: "text", Text: c}}
	case []interface{}:
		var parts []provider.InternalContentPart
		for _, item := range c {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "input_text", "output_text", "text":
				if text, _ := m["text"].(string); text != "" {
					parts = append(parts, provider.InternalContentPart{Type: "text", Text: text})
				}
			case "input_image", "image_url":
				if imageURL, ok := m["image_url"].(string); ok && imageURL != "" {
					parts = append(parts, provider.InternalContentPart{Type: "image_url", ImageURL: &imageURL})
				} else if imageURL, ok := m["image_url"].(map[string]interface{}); ok {
					url, _ := imageURL["url"].(string)
					if url != "" {
						parts = append(parts, provider.InternalContentPart{Type: "image_url", ImageURL: &url})
					}
				}
			}
		}
		return parts
	default:
		return nil
	}
}

// parseOpenAIMessage converts a single OpenAI message object to InternalMessage.
func parseOpenAIMessage(msg map[string]interface{}) provider.InternalMessage {
	im := provider.InternalMessage{}
	im.Role, _ = msg["role"].(string)

	// Parse content
	im.Content = parseOpenAIContent(msg["content"])

	// Parse tool_calls
	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
		im.ToolCalls = make([]provider.InternalToolCall, 0, len(toolCalls))
		for _, tcRaw := range toolCalls {
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

	// Parse tool result (for role "tool")
	if im.Role == "tool" {
		im.ToolResult = &provider.InternalToolResult{
			ToolCallID: intfStr(msg["tool_call_id"]),
		}
		if c, ok := msg["content"].(string); ok {
			im.ToolResult.Content = c
		}
	}

	return im
}

// parseOpenAIContent converts OpenAI content (string or array) to InternalContentPart slice.
func parseOpenAIContent(content interface{}) []provider.InternalContentPart {
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

// parseOpenAIToolChoice converts OpenAI tool_choice to InternalToolChoice.
func parseOpenAIToolChoice(tc interface{}) *provider.InternalToolChoice {
	switch v := tc.(type) {
	case string:
		return &provider.InternalToolChoice{Type: v}
	case map[string]interface{}:
		itc := &provider.InternalToolChoice{}
		itc.Type, _ = v["type"].(string) // e.g. "function"
		if fn, ok := v["function"].(map[string]interface{}); ok {
			itc.Function, _ = fn["name"].(string)
		}
		return itc
	default:
		return &provider.InternalToolChoice{Type: "auto"}
	}
}

// internalToOpenAIChat converts InternalRequest to OpenAI Chat Completions JSON.
func internalToOpenAIChat(req *provider.InternalRequest) ([]byte, error) {
	oai := make(map[string]interface{})
	oai["model"] = req.Model
	oai["stream"] = req.Stream

	if req.MaxTokens != nil {
		oai["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		oai["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		oai["top_p"] = *req.TopP
	}
	if len(req.StopWords) > 0 {
		oai["stop"] = req.StopWords
	}

	// Messages
	messages := make([]interface{}, 0, len(req.Messages))
	for _, im := range req.Messages {
		messages = append(messages, buildOpenAIMessage(im))
	}
	oai["messages"] = messages

	// Tools
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type": it.Type,
				"function": map[string]interface{}{
					"name":        it.Name,
					"description": it.Description,
					"parameters":  it.Parameters,
				},
			})
		}
		oai["tools"] = tools
	}

	// ToolChoice
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "none", "required":
			oai["tool_choice"] = req.ToolChoice.Type
		default:
			oai["tool_choice"] = map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": req.ToolChoice.Function,
				},
			}
		}
	}

	return json.Marshal(oai)
}

// buildOpenAIMessage converts InternalMessage to OpenAI message map.
func buildOpenAIMessage(im provider.InternalMessage) map[string]interface{} {
	msg := map[string]interface{}{
		"role": im.Role,
	}

	// Content
	if len(im.Content) > 0 {
		if len(im.Content) == 1 && im.Content[0].Type == "text" && im.Content[0].ImageURL == nil {
			msg["content"] = im.Content[0].Text
		} else {
			content := make([]interface{}, 0, len(im.Content))
			for _, part := range im.Content {
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
	} else if im.Role != "tool" {
		msg["content"] = ""
	}

	// ToolCalls
	if len(im.ToolCalls) > 0 {
		tcs := make([]interface{}, 0, len(im.ToolCalls))
		for i, itc := range im.ToolCalls {
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

	// Tool result fields
	if im.ToolResult != nil {
		msg["tool_call_id"] = im.ToolResult.ToolCallID
		msg["content"] = im.ToolResult.Content
	}

	return msg
}

// internalToResponses converts InternalRequest to OpenAI Responses API format.
func internalToResponses(req *provider.InternalRequest) ([]byte, error) {
	resp := make(map[string]interface{})
	resp["model"] = req.Model
	if req.Stream {
		resp["stream"] = true
	}
	if req.Temperature != nil {
		resp["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		resp["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		resp["max_output_tokens"] = *req.MaxTokens
	}

	// Convert messages to input + instructions
	var instructions string
	var input []interface{}

	for _, im := range req.Messages {
		switch im.Role {
		case "system":
			if len(im.Content) > 0 && im.Content[0].Type == "text" {
				if instructions != "" {
					instructions += "\n\n"
				}
				instructions += im.Content[0].Text
			}
		case "developer":
			if len(im.Content) > 0 && im.Content[0].Type == "text" {
				if instructions != "" {
					instructions += "\n\n"
				}
				instructions += im.Content[0].Text
			}
		default:
			input = append(input, buildOpenAIMessage(im))
		}
	}

	if instructions != "" {
		resp["instructions"] = instructions
	}
	resp["input"] = input

	// Tools
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type": it.Type,
				"function": map[string]interface{}{
					"name":        it.Name,
					"description": it.Description,
					"parameters":  it.Parameters,
				},
			})
		}
		resp["tools"] = tools
	}

	// ToolChoice
	if req.ToolChoice != nil {
		resp["tool_choice"] = req.ToolChoice.Type
	}

	return json.Marshal(resp)
}

func intfStr(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
