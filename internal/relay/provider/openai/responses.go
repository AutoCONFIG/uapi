package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- Chat Completions → Responses API request conversion ---

func ChatToResponses(body []byte) ([]byte, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse chat request: %w", err)
	}

	resp := make(map[string]interface{})
	resp["model"] = req["model"]

	// stream
	if s, ok := req["stream"]; ok {
		resp["stream"] = s
	}

	// temperature
	if v, ok := req["temperature"]; ok {
		resp["temperature"] = v
	}
	// top_p
	if v, ok := req["top_p"]; ok {
		resp["top_p"] = v
	}

	// max_output_tokens (from max_tokens)
	if v, ok := req["max_tokens"]; ok {
		resp["max_output_tokens"] = v
	}

	// Convert messages to input + instructions
	if messagesRaw, ok := req["messages"]; ok {
		messages, ok := messagesRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("messages is not an array")
		}

		var instructions string
		var input []interface{}

		for _, msgRaw := range messages {
			msg, ok := msgRaw.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			switch role {
			case "system":
				if content := extractContent(msg); content != "" {
					instructions = content
				}
			case "developer":
				if content := extractContent(msg); content != "" {
					if instructions != "" {
						instructions += "\n\n"
					}
					instructions += content
				}
			default:
				input = append(input, msg)
			}
		}

		if instructions != "" {
			resp["instructions"] = instructions
		}
		resp["input"] = input
	}

	// tool_choice
	if v, ok := req["tool_choice"]; ok {
		resp["tool_choice"] = v
	}

	// tools → tools (same format for basic usage)
	if v, ok := req["tools"]; ok {
		resp["tools"] = v
	}

	return json.Marshal(resp)
}

// --- Responses API → Chat Completions response conversion (non-stream) ---

func ResponsesToChat(body []byte) ([]byte, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse responses body: %w", err)
	}

	chatResp := make(map[string]interface{})
	chatResp["id"] = resp["id"]
	chatResp["object"] = "chat.completion"
	chatResp["model"] = resp["model"]
	chatResp["created"] = 0

	// Convert output items to choices
	choices := convertOutputToChoices(resp)
	chatResp["choices"] = choices

	// Usage
	if usageRaw, ok := resp["usage"]; ok {
		chatResp["usage"] = convertResponsesUsage(usageRaw)
	}

	return json.Marshal(chatResp)
}

// --- Responses API → Chat Completions SSE stream conversion ---

func StreamResponsesToChat(sseBody []byte) []byte {
	lines := strings.Split(string(sseBody), "\n")
	var outLines []string
	var respID, model string
	roleSent := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			// Skip non-data lines (event: types, empty SSE framing lines)
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			outLines = append(outLines, "data: [DONE]")
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			outLines = append(outLines, line)
			continue
		}

		// Extract metadata from response.created or any event with these fields
		if id, ok := event["response_id"].(string); ok && id != "" {
			respID = id
		}
		if m, ok := event["model"].(string); ok && m != "" {
			model = m
		}
		// Also try top-level response object
		if respObj, ok := event["response"].(map[string]interface{}); ok {
			if id, ok := respObj["id"].(string); ok {
				respID = id
			}
			if m, ok := respObj["model"].(string); ok {
				model = m
			}
		}

		eventType, _ := event["type"].(string)

		switch {
		case eventType == "response.output_text.delta":
			delta := event["delta"]
			// Send role chunk first
			if !roleSent {
				roleSent = true
				roleChunk := buildStreamChunk(respID, model, map[string]interface{}{
					"role": "assistant",
				}, "")
				outLines = append(outLines, "data: "+roleChunk)
			}
			chunk := buildStreamChunk(respID, model, map[string]interface{}{
				"content": delta,
			}, "")
			outLines = append(outLines, "data: "+chunk)

		case eventType == "response.output_text.done":
			// Text complete, no action needed for chat format

		case eventType == "response.function_call_arguments.delta":
			args := event["delta"]
			outIdx := 0
			if v, ok := event["output_index"].(float64); ok {
				outIdx = int(v)
			}
			// Send role chunk if not sent yet
			if !roleSent {
				roleSent = true
				roleChunk := buildStreamChunk(respID, model, map[string]interface{}{
					"role": "assistant",
				}, "")
				outLines = append(outLines, "data: "+roleChunk)
			}
			callID, _ := event["call_id"].(string)
			chunk := buildStreamToolChunk(respID, model, outIdx, callID, nil, args)
			outLines = append(outLines, "data: "+chunk)

		case eventType == "response.function_call_arguments.done":
			// Arguments complete, no action needed

		case eventType == "response.output_item.added":
			// Handle function_call item being added — send tool_calls delta with name
			item, _ := event["item"].(map[string]interface{})
			itemType, _ := item["type"].(string)
			if itemType == "function_call" {
				if !roleSent {
					roleSent = true
					roleChunk := buildStreamChunk(respID, model, map[string]interface{}{
						"role": "assistant",
					}, "")
					outLines = append(outLines, "data: "+roleChunk)
				}
				outIdx := 0
				if v, ok := event["output_index"].(float64); ok {
					outIdx = int(v)
				}
				callID, _ := item["call_id"].(string)
				fnName, _ := item["name"].(string)
				chunk := buildStreamToolChunk(respID, model, outIdx, callID, fnName, "")
				outLines = append(outLines, "data: "+chunk)
			}

		case eventType == "response.completed" || eventType == "response.done":
			var usage interface{}
			if respObj, ok := event["response"].(map[string]interface{}); ok {
				if u, ok := respObj["usage"]; ok {
					usage = convertResponsesUsage(u)
				}
			}
			chunk := buildStreamChunk(respID, model, map[string]interface{}{}, "stop")
			if usage != nil {
				// Add usage to the chunk
				var chunkMap map[string]interface{}
				json.Unmarshal([]byte(chunk), &chunkMap)
				chunkMap["usage"] = usage
				if b, err := json.Marshal(chunkMap); err == nil {
					chunk = string(b)
				}
			}
			outLines = append(outLines, "data: "+chunk)

		default:
			// Pass through unrecognized events
			outLines = append(outLines, line)
		}
	}

	return []byte(strings.Join(outLines, "\n\n"))
}

// --- Helpers ---

func extractContent(msg map[string]interface{}) string {
	if c, ok := msg["content"].(string); ok {
		return c
	}
	// content can be an array of content parts
	if arr, ok := msg["content"].([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
				if t, ok := m["content"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func convertOutputToChoices(resp map[string]interface{}) []interface{} {
	outputRaw, ok := resp["output"]
	if !ok {
		return []interface{}{buildChoice(0, map[string]interface{}{
			"role":    "assistant",
			"content": "",
		}, "stop")}
	}

	output, ok := outputRaw.([]interface{})
	if !ok {
		return []interface{}{buildChoice(0, map[string]interface{}{
			"role":    "assistant",
			"content": "",
		}, "stop")}
	}

	var messageContent []interface{}
	var toolCalls []interface{}

	for _, itemRaw := range output {
		item, ok := itemRaw.(map[string]interface{})
		if !ok {
			continue
		}
		itemType, _ := item["type"].(string)

		switch itemType {
		case "message":
			// Extract content from message output item
			if content, ok := item["content"].([]interface{}); ok {
				for _, c := range content {
					messageContent = append(messageContent, c)
				}
			} else if text, ok := item["content"].(string); ok {
				messageContent = append(messageContent, map[string]interface{}{
					"type": "text",
					"text": text,
				})
			}
		case "function_call":
			idx := len(toolCalls)
			toolCalls = append(toolCalls, map[string]interface{}{
				"index": idx,
				"id":    item["call_id"],
				"type":  "function",
				"function": map[string]interface{}{
					"name":      item["name"],
					"arguments": item["arguments"],
				},
			})
		}
	}

	msg := map[string]interface{}{
		"role": "assistant",
	}
	if len(messageContent) == 1 {
		if m, ok := messageContent[0].(map[string]interface{}); ok {
			if t, ok := m["text"].(string); ok {
				msg["content"] = t
			}
		}
	} else if len(messageContent) > 0 {
		msg["content"] = messageContent
	} else {
		msg["content"] = nil
	}

	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return []interface{}{buildChoice(0, msg, finishReason)}
}

func buildChoice(index int, message map[string]interface{}, finishReason string) map[string]interface{} {
	choice := map[string]interface{}{
		"index":         index,
		"message":       message,
		"finish_reason": finishReason,
	}
	return choice
}

func convertResponsesUsage(usageRaw interface{}) map[string]interface{} {
	usage, ok := usageRaw.(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
	}
	inputTokens, _ := toFloat(usage["input_tokens"])
	outputTokens, _ := toFloat(usage["output_tokens"])
	return map[string]interface{}{
		"prompt_tokens":     int(inputTokens),
		"completion_tokens": int(outputTokens),
		"total_tokens":      int(inputTokens + outputTokens),
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func buildStreamChunk(id, model string, delta map[string]interface{}, finishReason string) string {
	finishVal := interface{}(nil)
	if finishReason != "" {
		finishVal = finishReason
	}
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishVal,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func buildStreamToolChunk(id, model string, toolIndex int, callID, functionName, args interface{}) string {
	tc := map[string]interface{}{
		"index":    toolIndex,
		"id":       callID,
		"type":     "function",
		"function": map[string]interface{}{},
	}
	if functionName != nil && functionName != "" {
		tc["function"].(map[string]interface{})["name"] = functionName
	}
	if args != nil && args != "" {
		tc["function"].(map[string]interface{})["arguments"] = args
	}
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{tc},
				},
				"finish_reason": nil,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}
