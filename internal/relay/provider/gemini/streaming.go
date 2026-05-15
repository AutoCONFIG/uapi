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
