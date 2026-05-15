package anthropic

import (
	"encoding/json"
	"strings"
)

// anthropicStreamState tracks per-stream conversion state.
type anthropicStreamState struct {
	msgID       string
	model       string
	roleID      string
	inputTokens int
}

// convertLine converts a single Anthropic SSE data line to OpenAI Chat Completions SSE chunk.
// Returns nil if the line should be skipped.
func (s *anthropicStreamState) convertLine(line []byte) []byte {
	lineStr := strings.TrimSpace(string(line))

	// Skip non-data lines (event: xxx etc)
	if !strings.HasPrefix(lineStr, "data: ") {
		return nil
	}

	data := strings.TrimPrefix(lineStr, "data: ")
	if data == "[DONE]" {
		return nil // [DONE] is sent by streamAndForward's SendDone()
	}

	var event map[string]interface{}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_start":
		if msg, ok := event["message"].(map[string]interface{}); ok {
			if id, ok := msg["id"].(string); ok {
				s.msgID = id
			}
			if m, ok := msg["model"].(string); ok {
				s.model = m
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				s.inputTokens = toInt(usage["input_tokens"])
			}
		}
		if s.roleID == "" {
			s.roleID = s.msgID
			if s.roleID == "" {
				s.roleID = "chatcmpl-anthropic"
			}
		}
		return buildOpenAIChunk(s.roleID, s.model, map[string]interface{}{"role": "assistant"}, "")

	case "content_block_start":
		block, _ := event["content_block"].(map[string]interface{})
		blockType, _ := block["type"].(string)
		if blockType == "tool_use" {
			idx := 0
			if i, ok := event["index"].(float64); ok {
				idx = int(i)
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			tc := map[string]interface{}{
				"index": idx,
				"id":    id,
				"type":  "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": "",
				},
			}
			return buildOpenAIChunk(s.roleID, s.model,
				map[string]interface{}{"tool_calls": []interface{}{tc}}, "")
		}
		// Skip text block start (no delta content) and other types
		return nil

	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return nil
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "text_delta":
			text, _ := delta["text"].(string)
			return buildOpenAIChunk(s.roleID, s.model,
				map[string]interface{}{"content": text}, "")
		case "input_json_delta":
			partialJSON, _ := delta["partial_json"].(string)
			idx := 0
			if i, ok := event["index"].(float64); ok {
				idx = int(i)
			}
			tc := map[string]interface{}{
				"index": idx,
				"function": map[string]interface{}{
					"arguments": partialJSON,
				},
			}
			return buildOpenAIChunk(s.roleID, s.model,
				map[string]interface{}{"tool_calls": []interface{}{tc}}, "")
		case "thinking_delta":
			return nil
		}
		return nil

	case "message_delta":
		delta, _ := event["delta"].(map[string]interface{})
		stopReason, _ := delta["stop_reason"].(string)
		finishReason := mapFinishReason(stopReason)
		usage := map[string]interface{}{
			"prompt_tokens":     s.inputTokens,
			"completion_tokens": 0,
		}
		if u, ok := event["usage"].(map[string]interface{}); ok {
			usage = map[string]interface{}{
				"prompt_tokens":     s.inputTokens,
				"completion_tokens": toInt(u["output_tokens"]),
			}
		}
		chunk := map[string]interface{}{
			"id":      s.roleID,
			"object":  "chat.completion.chunk",
			"model":   s.model,
			"choices": []interface{}{
				map[string]interface{}{
					"index":         0,
					"delta":         map[string]interface{}{},
					"finish_reason": finishReason,
				},
			},
			"usage": usage,
		}
		b, _ := json.Marshal(chunk)
		return []byte("data: " + string(b) + "\n\n")

	default:
		// message_stop, ping, content_block_stop, error — skip
		return nil
	}
}

// convertAnthropicSSEBuffer converts a full buffered Anthropic SSE body to OpenAI SSE format.
func convertAnthropicSSEBuffer(sseBody []byte) []byte {
	state := &anthropicStreamState{}
	lines := strings.Split(string(sseBody), "\n")
	var outLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		converted := state.convertLine([]byte(line))
		if converted != nil {
			outLines = append(outLines, strings.TrimRight(string(converted), "\n"))
		}
	}

	if len(outLines) > 0 {
		return []byte(strings.Join(outLines, "\n\n") + "\n\ndata: [DONE]\n\n")
	}
	return sseBody
}

func buildOpenAIChunk(id, model string, delta map[string]interface{}, finishReason string) []byte {
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
	return []byte("data: " + string(b) + "\n\n")
}
