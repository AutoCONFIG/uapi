package gemini

import (
	"encoding/json"
	"strings"
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
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil
	}

	// Gemini may wrap chunks: {"method":"generateContentStream","response":[...]}
	if resp, ok := chunk["response"].([]interface{}); ok {
		var out []byte
		for _, r := range resp {
			if rMap, ok := r.(map[string]interface{}); ok {
				converted := s.convertChunk(rMap, model)
				if converted != nil {
					out = append(out, converted...)
				}
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

	candidates, _ := chunk["candidates"].([]interface{})
	if len(candidates) == 0 {
		if um, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			pt := toInt(um["promptTokenCount"])
			ct := toInt(um["candidatesTokenCount"])
			return buildGeminiChunkWithUsage(s.roleID, s.model, map[string]interface{}{}, "", pt, ct)
		}
		return nil
	}

	cand, _ := candidates[0].(map[string]interface{})
	cont, _ := cand["content"].(map[string]interface{})
	parts, _ := cont["parts"].([]interface{})
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
			continue
		}
		if text, ok := part["text"].(string); ok {
			contentText += text
		}
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			args := "{}"
			if a, err := json.Marshal(fc["args"]); err == nil {
				args = string(a)
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"index": len(toolCalls),
				"id":    "call_" + randomHex(12),
				"type":  "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": args,
				},
			})
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
			pt := toInt(um["promptTokenCount"])
			ct := toInt(um["candidatesTokenCount"])
			result = append(result, buildGeminiChunkWithUsage(s.roleID, s.model,
				map[string]interface{}{}, finishReason, pt, ct)...)
		}
	}

	return result
}

func buildGeminiChunk(id, model string, delta map[string]interface{}, finishReason string) []byte {
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

func buildGeminiChunkWithUsage(id, model string, delta map[string]interface{}, finishReason string, pt, ct int) []byte {
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
	lines := strings.Split(string(sseBody), "\n")
	var outLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		converted := state.convertLine([]byte(line), "")
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

// --- Reverse SSE conversion: OpenAI SSE → Gemini SSE ---

// geminiReverseState tracks state for converting OpenAI SSE chunks back to Gemini format.
type geminiReverseState struct {
	model string
}

func newGeminiReverseState() *geminiReverseState {
	return &geminiReverseState{}
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
		return []byte("data: [DONE]\n\n")
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
		return nil
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
	if content, ok := choice.Delta["content"].(string); ok && content != "" {
		parts = append(parts, map[string]interface{}{
			"text": content,
		})
	}

	// Tool calls → functionCall parts
	if tcs, ok := choice.Delta["tool_calls"].([]interface{}); ok {
		for _, tcRaw := range tcs {
			tc, ok := tcRaw.(map[string]interface{})
			if !ok {
				continue
			}
			fn, _ := tc["function"].(map[string]interface{})
			if fn == nil {
				continue
			}
			name, _ := fn["name"].(string)
			argsStr, _ := fn["arguments"].(string)
			args := map[string]interface{}{}
			if argsStr != "" {
				_ = json.Unmarshal([]byte(argsStr), &args)
			}
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": name,
					"args": args,
				},
			})
		}
	}

	// Role-only delta (skip, Gemini doesn't need it)
	if len(parts) == 0 {
		if choice.FinishReason == nil && chunk.Usage == nil {
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
