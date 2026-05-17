package anthropic

import (
	"encoding/json"
	"fmt"
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

// --- Reverse SSE conversion: OpenAI SSE → Anthropic SSE ---

// anthropicReverseState tracks state for converting OpenAI SSE chunks back to Anthropic SSE events.
type anthropicReverseState struct {
	started  bool
	model    string
	msgID    string
	blockIdx int
}

func newAnthropicReverseState() *anthropicReverseState {
	return &anthropicReverseState{
		msgID: fmt.Sprintf("msg_%s", randomAnthropicHex(24)),
	}
}

// convertReverseLine converts a single OpenAI SSE data line to Anthropic SSE events.
// It returns one or more SSE events (possibly multi-line), or nil to skip the line.
func (s *anthropicReverseState) convertReverseLine(line []byte) []byte {
	lineStr := strings.TrimSpace(string(line))

	// Pass through non-data lines
	if !strings.HasPrefix(lineStr, "data: ") {
		return nil
	}
	data := strings.TrimPrefix(lineStr, "data: ")
	if data == "[DONE]" {
		return []byte("data: [DONE]\n\n")
	}

	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int                    `json:"index"`
			Delta        map[string]interface{} `json:"delta"`
			FinishReason *string                `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil
	}

	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.ID != "" {
		s.msgID = chunk.ID
	}

	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]
	var result []byte

	// Emit message_start + content_block_start on first content chunk
	if !s.started {
		s.started = true
		result = append(result, s.buildMessageStart()...)
		result = append(result, s.buildContentBlockStart("text", 0)...)
		s.blockIdx = 0
	}

	// Handle tool_calls in delta
	if tcs, ok := choice.Delta["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
		for _, tcRaw := range tcs {
			tc, ok := tcRaw.(map[string]interface{})
			if !ok {
				continue
			}
			// If tool call has a name, it's a new tool_use block start
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					// Close previous text block
					if s.blockIdx == 0 {
						result = append(result, s.buildContentBlockStop(s.blockIdx)...)
						s.blockIdx++
					}
					id, _ := tc["id"].(string)
					if id == "" {
						id = fmt.Sprintf("toolu_%s", randomAnthropicHex(24))
					}
					result = append(result, s.buildToolUseBlockStart(s.blockIdx, id, name)...)
				}
				// Tool call arguments delta
				if args, ok := fn["arguments"].(string); ok && args != "" {
					result = append(result, s.buildInputJSONDelta(s.blockIdx, args)...)
				}
			}
		}
		// Check finish
		if choice.FinishReason != nil {
			result = append(result, s.buildContentBlockStop(s.blockIdx)...)
			result = append(result, s.buildMessageDelta(*choice.FinishReason, chunk.Usage)...)
		}
		if len(result) > 0 {
			return result
		}
		return nil
	}

	// Handle content delta
	if content, ok := choice.Delta["content"].(string); ok && content != "" {
		result = append(result, s.buildTextDelta(s.blockIdx, content)...)
	}

	// Handle role-only delta (first chunk, already handled by message_start)
	if len(choice.Delta) == 0 || (len(choice.Delta) == 1 && choice.Delta["role"] != nil) {
		if choice.FinishReason == nil && len(result) == 0 {
			return nil
		}
	}

	// Handle finish
	if choice.FinishReason != nil {
		result = append(result, s.buildContentBlockStop(s.blockIdx)...)
		result = append(result, s.buildMessageDelta(*choice.FinishReason, chunk.Usage)...)
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *anthropicReverseState) buildMessageStart() []byte {
	event := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":      s.msgID,
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{},
			"model":   s.model,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: message_start\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildContentBlockStart(blockType string, idx int) []byte {
	event := map[string]interface{}{
		"type":  "content_block_start",
		"index": idx,
		"content_block": map[string]interface{}{
			"type": blockType,
			"text": "",
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: content_block_start\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildToolUseBlockStart(idx int, id, name string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_start",
		"index": idx,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: content_block_start\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildTextDelta(idx int, text string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": idx,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: content_block_delta\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildInputJSONDelta(idx int, partialJSON string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": idx,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: content_block_delta\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildContentBlockStop(idx int) []byte {
	event := map[string]interface{}{
		"type":  "content_block_stop",
		"index": idx,
	}
	b, _ := json.Marshal(event)
	return []byte("event: content_block_stop\ndata: " + string(b) + "\n\n")
}

func (s *anthropicReverseState) buildMessageDelta(finishReason string, usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}) []byte {
	stopReason := mapFinishReasonFromOpenAI(finishReason)
	outputTokens := 0
	if usage != nil {
		outputTokens = usage.CompletionTokens
	}
	event := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason": stopReason,
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	}
	b, _ := json.Marshal(event)
	return []byte("event: message_delta\ndata: " + string(b) + "\n\nevent: message_stop\ndata: {}\n\n")
}

// mapFinishReasonFromOpenAI maps OpenAI finish reasons back to Anthropic stop_reason.
func mapFinishReasonFromOpenAI(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// randomAnthropicHex generates a random hex string for IDs.
func randomAnthropicHex(n int) string {
	b := make([]byte, n)
	// Use crypto/rand for uniqueness
	if _, err := cryptoRandRead(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hexEncodeToString(b)
}
