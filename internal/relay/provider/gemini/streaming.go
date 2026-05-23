package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// geminiStreamState tracks per-stream conversion state.
type geminiStreamState struct {
	respID   string
	model    string
	roleID   string
	roleSent bool
}

// convertLine converts a single Gemini response chunk line to OpenAI SSE format.
func (s *geminiStreamState) convertLine(line []byte, model string) []byte {
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" {
		return nil
	}

	// Handle "data: " prefix if present
	data := lineStr
	if strings.HasPrefix(lineStr, "data: ") {
		data = strings.TrimPrefix(lineStr, "data: ")
	}
	if data == "[DONE]" {
		return nil // [DONE] is sent by streamAndForward's SendDone()
	}

	var chunk map[string]interface{}
	if err := provider.DecodeJSONUseNumber([]byte(data), &chunk); err != nil {
		return buildGeminiStreamError(s.roleID, model, "gemini streaming response is not valid JSON")
	}

	// Gemini may wrap chunks: {"method":"generateContentStream","response":[...]}
	if wrapped, ok := chunk["response"]; ok {
		respObj, ok := wrapped.(map[string]interface{})
		if ok {
			return s.convertChunk(respObj, model)
		}
		resp, ok := wrapped.([]interface{})
		if !ok {
			return buildGeminiStreamError(s.roleID, model, "gemini streaming response wrapper must be an object or array")
		}
		var out []byte
		for _, r := range resp {
			rMap, ok := r.(map[string]interface{})
			if !ok {
				return buildGeminiStreamError(s.roleID, model, "gemini streaming response wrapper entries must be objects")
			}
			converted := s.convertChunk(rMap, model)
			if converted != nil {
				out = append(out, converted...)
			}
		}
		return out
	}

	return s.convertChunk(chunk, model)
}

func (s *geminiStreamState) convertChunk(chunk map[string]interface{}, model string) []byte {
	if s.roleID == "" {
		s.roleID = "chatcmpl-gemini"
		s.model = model
	}

	candidatesRaw, exists := chunk["candidates"]
	candidates, _ := candidatesRaw.([]interface{})
	if exists && candidates == nil {
		return buildGeminiStreamError(s.roleID, s.model, "gemini streaming candidates must be an array")
	}
	if len(candidates) == 0 {
		if um, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			pt := provider.ToInt(um["promptTokenCount"])
			ct := provider.ToInt(um["candidatesTokenCount"])
			return buildGeminiChunkWithUsage(s.roleID, s.model, map[string]interface{}{}, "", pt, ct)
		}
		return nil
	}

	cand, ok := candidates[0].(map[string]interface{})
	if !ok {
		return buildGeminiStreamError(s.roleID, s.model, "gemini streaming candidates entries must be objects")
	}
	cont, ok := cand["content"].(map[string]interface{})
	if !ok {
		return buildGeminiStreamError(s.roleID, s.model, "gemini streaming candidate content must be an object")
	}
	parts, ok := cont["parts"].([]interface{})
	if !ok {
		return buildGeminiStreamError(s.roleID, s.model, "gemini streaming candidate content parts must be an array")
	}
	finishReason := ""
	if fr, ok := cand["finishReason"].(string); ok {
		finishReason = mapGeminiFinishReason(fr)
	}

	var result []byte

	// Send role on first chunk
	if !s.roleSent {
		s.roleSent = true
		result = append(result, buildGeminiChunk(s.roleID, s.model,
			map[string]interface{}{"role": "assistant"}, "")...)
	}

	var contentText string
	var toolCalls []interface{}

	for _, partRaw := range parts {
		part, ok := partRaw.(map[string]interface{})
		if !ok {
			return buildGeminiStreamError(s.roleID, s.model, "gemini streaming parts entries must be objects")
		}
		if err := validateGeminiPartKeys(part); err != nil {
			return buildGeminiStreamError(s.roleID, s.model, err.Error())
		}
		handled := false
		if text, ok := part["text"].(string); ok {
			handled = true
			contentText += text
		} else if _, exists := part["text"]; exists {
			return buildGeminiStreamError(s.roleID, s.model, "gemini streaming text part requires string text")
		}
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			handled = true
			if err := validateAllowedKeys(fc, "gemini streaming functionCall", "name", "args"); err != nil {
				return buildGeminiStreamError(s.roleID, s.model, err.Error())
			}
			name, _ := fc["name"].(string)
			if name == "" {
				return buildGeminiStreamError(s.roleID, s.model, "gemini streaming functionCall requires name")
			}
			args := "{}"
			if argsVal, exists := fc["args"]; exists {
				a, err := json.Marshal(argsVal)
				if err != nil {
					return buildGeminiStreamError(s.roleID, s.model, "gemini streaming functionCall args must be JSON-serializable")
				}
				args = string(a)
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"index": len(toolCalls),
				"id":    "call_" + provider.RandomHex(12),
				"type":  "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": args,
				},
			})
		}
		if !handled {
			return buildGeminiStreamError(s.roleID, s.model, "gemini streaming response part cannot be converted to non-gemini downstream formats")
		}
	}

	delta := map[string]interface{}{}
	if contentText != "" {
		delta["content"] = contentText
	}
	if len(toolCalls) > 0 {
		delta["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}

	chunkBytes := buildGeminiChunk(s.roleID, s.model, delta, finishReason)
	result = append(result, chunkBytes...)

	// Add usage from last chunk
	if finishReason != "" {
		if um, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			pt := provider.ToInt(um["promptTokenCount"])
			ct := provider.ToInt(um["candidatesTokenCount"])
			result = append(result, buildGeminiChunkWithUsage(s.roleID, s.model,
				map[string]interface{}{}, finishReason, pt, ct)...)
		}
	}

	return result
}

func buildGeminiStreamError(id, model, message string) []byte {
	if id == "" {
		id = "chatcmpl-gemini"
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

func buildGeminiChunk(id, model string, delta map[string]interface{}, finishReason string) []byte {
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

func buildGeminiChunkWithUsage(id, model string, delta map[string]interface{}, finishReason string, pt, ct int) []byte {
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
		"usage": map[string]interface{}{
			"prompt_tokens":     pt,
			"completion_tokens": ct,
			"total_tokens":      pt + ct,
		},
	}
	b, _ := json.Marshal(chunk)
	return []byte("data: " + string(b) + "\n\n")
}

// convertGeminiSSEBuffer converts a full buffered Gemini SSE body to OpenAI SSE format.
func convertGeminiSSEBuffer(sseBody []byte) []byte {
	state := &geminiStreamState{}
	var outLines []string

	for _, event := range splitGeminiSSEEvents(sseBody) {
		converted := state.convertLine(normalizeGeminiSSEEvent(event), "")
		if converted != nil {
			for _, chunk := range strings.Split(strings.TrimRight(string(converted), "\n"), "\n\n") {
				if chunk != "" {
					outLines = append(outLines, chunk)
				}
			}
		}
	}

	if len(outLines) > 0 {
		return []byte(strings.Join(outLines, "\n\n") + "\n\ndata: [DONE]\n\n")
	}
	return sseBody
}

func normalizeGeminiSSEEvent(event []byte) []byte {
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

func splitGeminiSSEEvents(buf []byte) [][]byte {
	parts := strings.Split(strings.TrimRight(string(buf), "\n"), "\n\n")
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, []byte(part+"\n\n"))
		}
	}
	return out
}

// --- Reverse SSE conversion: OpenAI SSE → Gemini SSE ---

// geminiReverseState tracks state for converting OpenAI SSE chunks back to Gemini format.
type geminiReverseState struct {
	model        string
	toolCalls    map[int]*geminiToolCall
	toolOrder    []int
	toolsFlushed bool
}

type geminiToolCall struct {
	Name      string
	Arguments strings.Builder
}

func newGeminiReverseState() *geminiReverseState {
	return &geminiReverseState{toolCalls: make(map[int]*geminiToolCall)}
}

func NewReverseStreamConverter() func([]byte) []byte {
	state := newGeminiReverseState()
	return state.convertReverseLine
}

// convertReverseLine converts a single OpenAI SSE data line to Gemini SSE format.
// Gemini streaming uses data-only SSE lines with JSON payloads containing candidates.
func (s *geminiReverseState) convertReverseLine(line []byte) []byte {
	lineStr := strings.TrimSpace(string(line))

	if !strings.HasPrefix(lineStr, "data: ") {
		return nil
	}
	data := strings.TrimPrefix(lineStr, "data: ")
	if data == "[DONE]" {
		return nil
	}

	var chunk struct {
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
		return buildGeminiReverseStreamError("invalid OpenAI SSE JSON for Gemini conversion")
	}

	if chunk.Model != "" {
		s.model = chunk.Model
	}

	if len(chunk.Choices) == 0 {
		// Usage-only chunk
		if chunk.Usage != nil {
			return s.buildUsageChunk(chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens)
		}
		return nil
	}

	choice := chunk.Choices[0]
	var result []byte

	// Build Gemini candidate
	parts := []interface{}{}

	// Text content
	if hasOpenAIReasoningDelta(choice.Delta) {
		return buildGeminiReverseStreamError("OpenAI reasoning deltas cannot be converted to Gemini text parts")
	}
	if content := openAITextDelta(choice.Delta); content != "" {
		parts = append(parts, map[string]interface{}{
			"text": content,
		})
	}

	// Tool calls → functionCall parts
	if tcs, ok := choice.Delta["tool_calls"].([]interface{}); ok {
		for _, tcRaw := range tcs {
			tc, ok := tcRaw.(map[string]interface{})
			if !ok {
				return buildGeminiReverseStreamError("OpenAI tool_calls entries must be objects for Gemini conversion")
			}
			fn, _ := tc["function"].(map[string]interface{})
			if fn == nil {
				return buildGeminiReverseStreamError("OpenAI tool_call function must be an object for Gemini conversion")
			}
			idx := provider.ToInt(tc["index"])
			state := s.toolCalls[idx]
			if state == nil {
				state = &geminiToolCall{}
				s.toolCalls[idx] = state
				s.toolOrder = append(s.toolOrder, idx)
			}
			name, _ := fn["name"].(string)
			if name != "" {
				state.Name = name
			}
			if argsVal, exists := fn["arguments"]; exists {
				argsStr, ok := argsVal.(string)
				if !ok {
					return buildGeminiReverseStreamError("OpenAI tool call arguments must be a string for Gemini conversion")
				}
				if argsStr != "" {
					state.Arguments.WriteString(argsStr)
				}
			}
		}
	}

	if choice.FinishReason != nil && len(s.toolCalls) > 0 && !s.toolsFlushed {
		for _, idx := range s.toolOrder {
			state := s.toolCalls[idx]
			if state == nil || state.Name == "" {
				return buildGeminiReverseStreamError("OpenAI tool call is missing function name for Gemini conversion")
			}
			var args json.RawMessage = []byte("{}")
			if state.Arguments.Len() > 0 {
				if !json.Valid([]byte(state.Arguments.String())) {
					return buildGeminiReverseStreamError("OpenAI tool call arguments must be valid JSON for Gemini conversion")
				}
				args = json.RawMessage(state.Arguments.String())
			}
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": state.Name,
					"args": args,
				},
			})
		}
		s.toolsFlushed = true
	}

	// Role-only delta (skip, Gemini doesn't need it)
	if len(parts) == 0 {
		if choice.FinishReason == nil && (chunk.Usage == nil || (chunk.Usage.PromptTokens == 0 && chunk.Usage.CompletionTokens == 0)) {
			return nil
		}
	}

	candidate := map[string]interface{}{
		"content": map[string]interface{}{
			"parts": parts,
			"role":  "model",
		},
	}

	if choice.FinishReason != nil {
		candidate["finishReason"] = mapOpenAIFinishReasonToGemini(*choice.FinishReason)
	}

	gemChunk := map[string]interface{}{
		"candidates": []interface{}{candidate},
	}

	// Include usage in the final chunk
	if chunk.Usage != nil {
		gemChunk["usageMetadata"] = map[string]interface{}{
			"promptTokenCount":     chunk.Usage.PromptTokens,
			"candidatesTokenCount": chunk.Usage.CompletionTokens,
			"totalTokenCount":      chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens,
		}
	}

	b, _ := json.Marshal(gemChunk)
	result = append(result, []byte("data: "+string(b)+"\n\n")...)
	return result
}

func buildGeminiReverseStreamError(message string) []byte {
	payload := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    400,
			"message": message,
			"status":  "INVALID_ARGUMENT",
		},
	}
	b, _ := json.Marshal(payload)
	return []byte(fmt.Sprintf("event: error\ndata: %s\n\n", b))
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

func (s *geminiReverseState) buildUsageChunk(pt, ct int) []byte {
	chunk := map[string]interface{}{
		"usageMetadata": map[string]interface{}{
			"promptTokenCount":     pt,
			"candidatesTokenCount": ct,
			"totalTokenCount":      pt + ct,
		},
	}
	b, _ := json.Marshal(chunk)
	return []byte("data: " + string(b) + "\n\n")
}

// mapOpenAIFinishReasonToGemini maps OpenAI finish reasons to Gemini finish reasons.
func mapOpenAIFinishReasonToGemini(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}
