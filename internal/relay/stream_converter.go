package relay

import (
	"encoding/json"
	"strings"
)

// StreamToNonStream converts a buffered SSE streaming response into a
// standard non-streaming Chat Completions JSON response.
func StreamToNonStream(sseBody []byte) []byte {
	lines := strings.Split(string(sseBody), "\n")

	var contentBuilder strings.Builder
	var toolCalls []map[string]interface{}
	toolArgsBuilders := make(map[int]*strings.Builder)

	var finishReason string
	var usage map[string]interface{}
	var model, respID string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if id, ok := chunk["id"].(string); ok && id != "" {
			respID = id
		}
		if m, ok := chunk["model"].(string); ok && m != "" {
			model = m
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
					toolCalls = append(toolCalls, map[string]interface{}{
						"index": len(toolCalls),
					})
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
	for i, tc := range toolCalls {
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			fn["arguments"] = toolArgsBuilders[i].String()
		}
	}

	// Build response
	if finishReason == "" {
		finishReason = "stop"
	}

	msg := map[string]interface{}{
		"role":    "assistant",
		"content": contentBuilder.String(),
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		msg["content"] = nil
		finishReason = "tool_calls"
	}

	resp := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion",
		"created": 0,
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
	return b
}
