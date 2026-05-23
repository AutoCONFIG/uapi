package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// anthropicStreamState tracks per-stream conversion state.
type anthropicStreamState struct {
	msgID       string
	model       string
	roleID      string
	inputTokens int
	toolIndexes map[int]int
	toolStarted map[int]bool
	nextToolIdx int
}

// convertLine converts a single Anthropic SSE data line to OpenAI Chat Completions API SSE chunk.
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
		return buildAnthropicStreamError(s.roleID, s.model, "anthropic streaming event is not valid JSON")
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_start":
		msg, ok := event["message"].(map[string]interface{})
		if !ok {
			return buildAnthropicStreamError(s.roleID, s.model, "anthropic message_start requires message object")
		}
		if id, ok := msg["id"].(string); ok {
			s.msgID = id
		}
		if m, ok := msg["model"].(string); ok {
			s.model = m
		}
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			s.inputTokens = provider.ToInt(usage["input_tokens"])
		}
		if s.roleID == "" {
			s.roleID = s.msgID
			if s.roleID == "" {
				s.roleID = "chatcmpl-anthropic"
			}
		}
		return buildOpenAIChunk(s.roleID, s.model, map[string]interface{}{"role": "assistant"}, "")

	case "content_block_start":
		block, ok := event["content_block"].(map[string]interface{})
		if !ok {
			return buildAnthropicStreamError(s.roleID, s.model, "anthropic content_block_start requires content_block object")
		}
		if err := validateAnthropicContentBlockKeys(block); err != nil {
			return buildAnthropicStreamError(s.roleID, s.model, err.Error())
		}
		blockType, _ := block["type"].(string)
		if blockType == "tool_use" {
			idx, ok := s.requiredToolCallIndex(event)
			if !ok {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic streaming tool_use requires numeric index")
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if id == "" || name == "" {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic streaming tool_use requires id and name")
			}
			if s.toolStarted == nil {
				s.toolStarted = make(map[int]bool)
			}
			s.toolStarted[idx] = true
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
		if blockType != "text" {
			return buildAnthropicStreamError(s.roleID, s.model, fmt.Sprintf("anthropic streaming content block type %q cannot be converted to non-anthropic downstream formats", blockType))
		}
		// Skip text block start (no delta content)
		return nil

	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return buildAnthropicStreamError(s.roleID, s.model, "anthropic content_block_delta requires delta object")
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "text_delta":
			if err := validateAllowedKeys(delta, "anthropic stream text_delta", "type", "text"); err != nil {
				return buildAnthropicStreamError(s.roleID, s.model, err.Error())
			}
			text, ok := delta["text"].(string)
			if !ok {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic text_delta requires text")
			}
			return buildOpenAIChunk(s.roleID, s.model,
				map[string]interface{}{"content": text}, "")
		case "input_json_delta":
			if err := validateAllowedKeys(delta, "anthropic stream input_json_delta", "type", "partial_json"); err != nil {
				return buildAnthropicStreamError(s.roleID, s.model, err.Error())
			}
			partialJSON, ok := delta["partial_json"].(string)
			if !ok {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic input_json_delta requires partial_json")
			}
			idx, ok := s.requiredToolCallIndex(event)
			if !ok {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic input_json_delta requires numeric index")
			}
			if s.toolStarted == nil || !s.toolStarted[idx] {
				return buildAnthropicStreamError(s.roleID, s.model, "anthropic input_json_delta requires prior tool_use content_block_start")
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
			return buildAnthropicStreamError(s.roleID, s.model, "anthropic thinking_delta cannot be converted to non-anthropic downstream formats")
		}
		return buildAnthropicStreamError(s.roleID, s.model, fmt.Sprintf("anthropic streaming delta type %q cannot be converted to non-anthropic downstream formats", deltaType))

	case "message_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return buildAnthropicStreamError(s.roleID, s.model, "anthropic message_delta requires delta object")
		}
		stopReason, _ := delta["stop_reason"].(string)
		finishReason := mapFinishReason(stopReason)
		usage := map[string]interface{}{
			"prompt_tokens":     s.inputTokens,
			"completion_tokens": 0,
		}
		if u, ok := event["usage"].(map[string]interface{}); ok {
			usage = map[string]interface{}{
				"prompt_tokens":     s.inputTokens,
				"completion_tokens": provider.ToInt(u["output_tokens"]),
			}
		}
		chunk := map[string]interface{}{
			"id":     s.roleID,
			"object": "chat.completion.chunk",
			"model":  s.model,
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

	case "error":
		return buildAnthropicStreamError(s.roleID, s.model, anthropicStreamErrorMessage(event))

	default:
		// message_stop, ping, content_block_stop — skip
		if eventType != "message_stop" && eventType != "ping" && eventType != "content_block_stop" {
			return buildAnthropicStreamError(s.roleID, s.model, fmt.Sprintf("anthropic streaming event type %q cannot be converted to chat stream", eventType))
		}
		return nil
	}
}

func anthropicStreamErrorMessage(event map[string]interface{}) string {
	errObj, _ := event["error"].(map[string]interface{})
	if msg, _ := errObj["message"].(string); msg != "" {
		return msg
	}
	if typ, _ := errObj["type"].(string); typ != "" {
		return typ
	}
	return "anthropic upstream stream error"
}

func buildAnthropicStreamError(id, model, message string) []byte {
	if id == "" {
		id = "chatcmpl-anthropic"
	}
	payload := map[string]interface{}{
		"id":     id,
		"object": "error",
		"model":  model,
		"error": map[string]interface{}{
			"message": message,
			"type":    "conversion_error",
		},
	}
	b, _ := json.Marshal(payload)
	return []byte("data: " + string(b) + "\n\n")
}

func (s *anthropicStreamState) toolCallIndex(blockIndex int) int {
	if s.toolIndexes == nil {
		s.toolIndexes = make(map[int]int)
	}
	if idx, ok := s.toolIndexes[blockIndex]; ok {
		return idx
	}
	idx := s.nextToolIdx
	s.nextToolIdx++
	s.toolIndexes[blockIndex] = idx
	return idx
}

func (s *anthropicStreamState) requiredToolCallIndex(event map[string]interface{}) (int, bool) {
	raw, ok := event["index"]
	if !ok {
		return 0, false
	}
	idxFloat, ok := raw.(float64)
	if !ok || idxFloat < 0 || idxFloat != float64(int(idxFloat)) {
		return 0, false
	}
	return s.toolCallIndex(int(idxFloat)), true
}

// convertAnthropicSSEBuffer converts a full buffered Anthropic SSE body to OpenAI SSE format.
func convertAnthropicSSEBuffer(sseBody []byte) []byte {
	state := &anthropicStreamState{}
	var outLines []string

	for _, event := range splitAnthropicSSEEvents(sseBody) {
		converted := state.convertLine(normalizeAnthropicSSEEvent(event))
		if converted != nil {
			outLines = append(outLines, strings.TrimRight(string(converted), "\n"))
		}
	}

	if len(outLines) > 0 {
		return []byte(strings.Join(outLines, "\n\n") + "\n\ndata: [DONE]\n\n")
	}
	return sseBody
}

func normalizeAnthropicSSEEvent(event []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	dataParts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataParts = append(dataParts, data)
		}
	}
	if len(dataParts) == 0 {
		return nil
	}
	return []byte("data: " + strings.Join(dataParts, "\n") + "\n\n")
}

func splitAnthropicSSEEvents(buf []byte) [][]byte {
	parts := strings.Split(strings.TrimRight(string(buf), "\n"), "\n\n")
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, []byte(part+"\n\n"))
		}
	}
	return out
}

func buildOpenAIChunk(id, model string, delta map[string]interface{}, finishReason string) []byte {
	finishVal := interface{}(nil)
	if finishReason != "" {
		finishVal = finishReason
	}
	chunk := map[string]interface{}{
		"id":     id,
		"object": "chat.completion.chunk",
		"model":  model,
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
	started        bool
	completed      bool
	finishing      bool
	textOpen       bool
	finishReason   string
	model          string
	msgID          string
	blockIdx       int
	nextBlockIdx   int
	toolBlocks     map[int]int
	openToolBlocks map[int]bool
	toolBlockOrder []int
	toolStarted    map[int]bool
	toolArgs       map[int]*strings.Builder
}

func newAnthropicReverseState() *anthropicReverseState {
	return &anthropicReverseState{
		msgID:          fmt.Sprintf("msg_%s", provider.RandomHex(24)),
		toolBlocks:     make(map[int]int),
		openToolBlocks: make(map[int]bool),
		toolStarted:    make(map[int]bool),
		toolArgs:       make(map[int]*strings.Builder),
	}
}

func NewReverseStreamConverter() func([]byte) []byte {
	state := newAnthropicReverseState()
	return state.convertReverseLine
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
		if s.started && !s.completed {
			return s.buildCompletion(nil)
		}
		return nil
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
		return buildAnthropicReverseStreamError("invalid OpenAI SSE JSON for Anthropic conversion")
	}

	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.ID != "" {
		s.msgID = chunk.ID
	}

	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil && s.finishing && !s.completed {
			return s.buildCompletion(chunk.Usage)
		}
		return nil
	}

	choice := chunk.Choices[0]
	var result []byte

	if !s.started {
		s.started = true
		result = append(result, s.buildMessageStart()...)
	}

	// Handle tool_calls in delta
	if tcs, ok := choice.Delta["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
		for _, tcRaw := range tcs {
			tc, ok := tcRaw.(map[string]interface{})
			if !ok {
				return buildAnthropicReverseStreamError("OpenAI tool_calls entries must be objects for Anthropic conversion")
			}
			// If tool call has a name, it's a new tool_use block start
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				idx := provider.ToInt(tc["index"])
				if name, ok := fn["name"].(string); ok && name != "" {
					if s.textOpen {
						result = append(result, s.buildContentBlockStop(s.blockIdx)...)
						s.textOpen = false
					}
					blockIdx := s.toolBlockIndex(idx)
					id, _ := tc["id"].(string)
					if id == "" {
						id = fmt.Sprintf("toolu_%s", provider.RandomHex(24))
					}
					if !s.openToolBlocks[blockIdx] {
						result = append(result, s.buildToolUseBlockStart(blockIdx, id, name)...)
						s.openToolBlocks[blockIdx] = true
						s.toolBlockOrder = append(s.toolBlockOrder, blockIdx)
					}
					s.toolStarted[blockIdx] = true
				}
				// Tool call arguments delta
				if argsVal, exists := fn["arguments"]; exists {
					args, ok := argsVal.(string)
					if !ok {
						return buildAnthropicReverseStreamError("OpenAI tool call arguments must be a string for Anthropic conversion")
					}
					if args != "" {
						blockIdx := s.toolBlockIndex(idx)
						if !s.toolStarted[blockIdx] {
							return buildAnthropicReverseStreamError("OpenAI tool call arguments require a prior function name for Anthropic conversion")
						}
						if s.toolArgs[blockIdx] == nil {
							s.toolArgs[blockIdx] = &strings.Builder{}
						}
						s.toolArgs[blockIdx].WriteString(args)
						result = append(result, s.buildInputJSONDelta(s.toolBlockIndex(idx), args)...)
					}
				}
			} else {
				return buildAnthropicReverseStreamError("OpenAI tool_call function must be an object for Anthropic conversion")
			}
		}
		// Check finish
		if choice.FinishReason != nil {
			s.finishing = true
			s.finishReason = *choice.FinishReason
			if anthropicStreamUsageHasTokens(chunk.Usage) {
				result = append(result, s.buildCompletion(chunk.Usage)...)
			}
		}
		if len(result) > 0 {
			return result
		}
		return nil
	}

	// Handle content delta
	if hasOpenAIReasoningDelta(choice.Delta) {
		return buildAnthropicReverseStreamError("OpenAI reasoning deltas cannot be converted to Anthropic text deltas")
	}
	if content := openAITextDelta(choice.Delta); content != "" {
		if !s.textOpen {
			s.blockIdx = s.nextBlockIdx
			s.nextBlockIdx++
			s.textOpen = true
			result = append(result, s.buildContentBlockStart("text", s.blockIdx)...)
		}
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
		s.finishing = true
		s.finishReason = *choice.FinishReason
		if anthropicStreamUsageHasTokens(chunk.Usage) {
			result = append(result, s.buildCompletion(chunk.Usage)...)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

func buildAnthropicReverseStreamError(message string) []byte {
	payload := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": message,
		},
	}
	b, _ := json.Marshal(payload)
	return []byte(fmt.Sprintf("event: error\ndata: %s\n\n", b))
}

func anthropicStreamUsageHasTokens(usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}) bool {
	return usage != nil && (usage.PromptTokens > 0 || usage.CompletionTokens > 0)
}

func (s *anthropicReverseState) buildCompletion(usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}) []byte {
	if s.completed {
		return nil
	}
	s.completed = true
	finishReason := s.finishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	var out []byte
	if s.textOpen {
		out = append(out, s.buildContentBlockStop(s.blockIdx)...)
		s.textOpen = false
	}
	for _, blockIdx := range s.toolBlockOrder {
		if s.openToolBlocks[blockIdx] {
			if args := s.toolArgs[blockIdx]; args != nil && args.Len() > 0 && !json.Valid([]byte(args.String())) {
				return buildAnthropicReverseStreamError("OpenAI tool call arguments must be valid JSON for Anthropic conversion")
			}
			out = append(out, s.buildContentBlockStop(blockIdx)...)
			delete(s.openToolBlocks, blockIdx)
		}
	}
	out = append(out, s.buildMessageDelta(finishReason, usage)...)
	return out
}

func (s *anthropicReverseState) toolBlockIndex(toolIndex int) int {
	if idx, ok := s.toolBlocks[toolIndex]; ok {
		return idx
	}
	idx := s.nextBlockIdx
	s.nextBlockIdx++
	s.toolBlocks[toolIndex] = idx
	return idx
}

func openAITextDelta(delta map[string]interface{}) string {
	if text, ok := delta["content"].(string); ok && text != "" {
		return text
	}
	return ""
}

func hasOpenAIReasoningDelta(delta map[string]interface{}) bool {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if text, ok := delta[key].(string); ok && text != "" {
			return true
		}
	}
	return false
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
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	}
	b, _ := json.Marshal(event)
	stopEvent := map[string]interface{}{"type": "message_stop"}
	stopBytes, _ := json.Marshal(stopEvent)
	return []byte("event: message_delta\ndata: " + string(b) + "\n\nevent: message_stop\ndata: " + string(stopBytes) + "\n\n")
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
