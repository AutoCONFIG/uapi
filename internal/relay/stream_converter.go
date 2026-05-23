package relay

import (
	"encoding/json"
	"strings"
	"time"
)

// StreamToNonStream converts a buffered SSE streaming response into a
// standard non-streaming Chat Completions JSON response.
func StreamToNonStream(sseBody []byte) []byte {
	body, _ := StreamToNonStreamChecked(sseBody)
	return body
}

func StreamToNonStreamChecked(sseBody []byte) ([]byte, bool) {
	sseBody = normalizeSSEBufferForConversion(sseBody)
	var contentBuilder strings.Builder
	var toolCalls []map[string]interface{}
	toolArgsBuilders := make(map[int]*strings.Builder)

	var finishReason string
	complete := false
	var usage map[string]interface{}
	var model, respID string
	var created int64
	var streamErr map[string]interface{}

	for _, event := range splitSSEEvents(sseBody) {
		line := strings.TrimSpace(string(event))
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			complete = true
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if obj, _ := chunk["object"].(string); obj == "error" {
			streamErr = chunk
			continue
		}
		if errObj, ok := chunk["error"].(map[string]interface{}); ok {
			streamErr = map[string]interface{}{
				"error":  errObj,
				"object": "error",
			}
			continue
		}

		if id, ok := chunk["id"].(string); ok && id != "" {
			respID = id
		}
		if m, ok := chunk["model"].(string); ok && m != "" {
			model = m
		}
		if created == 0 {
			if v, ok := chunk["created"].(float64); ok && v > 0 {
				created = int64(v)
			}
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			// Maybe usage-only chunk
			if u, ok := chunk["usage"].(map[string]interface{}); ok {
				usage = u
			}
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
			complete = true
		}

		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Content delta
		if content, ok := delta["content"].(string); ok {
			contentBuilder.WriteString(content)
		}

		// Tool calls delta
		if tcList, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tcRaw := range tcList {
				tc, ok := tcRaw.(map[string]interface{})
				if !ok {
					continue
				}
				idx := 0
				if v, ok := tc["index"].(float64); ok {
					idx = int(v)
				}

				// Ensure we have a builder for this index
				if _, exists := toolArgsBuilders[idx]; !exists {
					toolArgsBuilders[idx] = &strings.Builder{}
				}
				// Grow toolCalls slice to fit this index
				for len(toolCalls) <= idx {
					toolCalls = append(toolCalls, map[string]interface{}{})
				}

				tcMap := toolCalls[idx]

				if fn, ok := tc["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok {
						tcMap["id"] = tc["id"]
						tcMap["type"] = "function"
						tcMap["function"] = map[string]interface{}{
							"name":      name,
							"arguments": "",
						}
					}
					if args, ok := fn["arguments"].(string); ok {
						toolArgsBuilders[idx].WriteString(args)
					}
				}
			}
		}

		// Usage from chunk
		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			usage = u
		}
	}

	// Finalize tool call arguments
	if streamErr != nil {
		b, _ := json.Marshal(streamErr)
		return b, true
	}

	for i, tc := range toolCalls {
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			fn["arguments"] = toolArgsBuilders[i].String()
		}
	}

	// Build response
	if finishReason == "" {
		finishReason = "stop"
	}
	if created == 0 {
		created = time.Now().Unix()
	}

	msg := map[string]interface{}{
		"role":    "assistant",
		"content": contentBuilder.String(),
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		if contentBuilder.Len() == 0 {
			msg["content"] = nil
		}
		if finishReason == "" || finishReason == "stop" {
			finishReason = "tool_calls"
		}
	}

	resp := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		resp["usage"] = usage
	}

	b, _ := json.Marshal(resp)
	return b, complete
}

func normalizeSSEBufferForConversion(sseBody []byte) []byte {
	lines := strings.Split(string(sseBody), "\n")
	var out []byte
	var event []byte
	flush := func() {
		if len(event) == 0 {
			return
		}
		if len(event) < 2 || string(event[len(event)-2:]) != "\n\n" {
			event = append(event, '\n')
		}
		normalized := normalizeSSEEventForConverterWithEvent(event)
		event = nil
		if len(normalized) > 0 {
			out = append(out, normalized...)
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		event = append(event, []byte(line)...)
		event = append(event, '\n')
	}
	flush()
	if len(out) == 0 {
		return sseBody
	}
	return out
}
