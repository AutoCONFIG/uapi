package relay

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type reasoningDetailAccum struct {
	typ       string
	id        string
	text      strings.Builder
	summary   strings.Builder
	signature string
	data      string
	encrypted string
	hasText   bool
	hasSum    bool
}

// StreamToNonStream converts a buffered SSE streaming response into a
// standard non-streaming Chat Completions JSON response.
func StreamToNonStream(sseBody []byte) []byte {
	body, _ := StreamToNonStreamChecked(sseBody)
	return body
}

func StreamToNonStreamChecked(sseBody []byte) ([]byte, bool) {
	sseBody = normalizeSSEBufferForConversion(sseBody)
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	reasoningDetails := make(map[int]*reasoningDetailAccum)
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

		if rawDetails, ok := delta["reasoning_details"].([]interface{}); ok && len(rawDetails) > 0 {
			for _, rawDetail := range rawDetails {
				detail, ok := rawDetail.(map[string]interface{})
				if !ok {
					continue
				}
				idx := len(reasoningDetails)
				if v, ok := detail["index"].(float64); ok {
					idx = int(v)
				}
				acc := reasoningDetails[idx]
				if acc == nil {
					acc = &reasoningDetailAccum{}
					reasoningDetails[idx] = acc
				}
				if typ, ok := detail["type"].(string); ok && typ != "" {
					acc.typ = typ
				}
				if id, ok := detail["id"].(string); ok && id != "" {
					acc.id = id
				}
				if text, ok := detail["text"].(string); ok && text != "" {
					acc.text.WriteString(text)
					acc.hasText = true
				}
				if summary, ok := detail["summary"].(string); ok && summary != "" {
					acc.summary.WriteString(summary)
					acc.hasSum = true
				}
				if signature, ok := detail["signature"].(string); ok && signature != "" {
					acc.signature = signature
				}
				if data, ok := detail["data"].(string); ok && data != "" {
					acc.data = data
				}
				if encrypted, ok := detail["encrypted_content"].(string); ok && encrypted != "" {
					acc.encrypted = encrypted
				}
			}
		} else if reasoning, ok := delta["reasoning_content"].(string); ok {
			reasoningBuilder.WriteString(reasoning)
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
	if len(reasoningDetails) > 0 {
		details, reasoning := finalizeStreamReasoningDetails(reasoningDetails)
		if reasoning != "" {
			msg["reasoning_content"] = reasoning
		}
		if len(details) > 0 {
			msg["reasoning_details"] = details
		}
	} else if reasoning := reasoningBuilder.String(); reasoning != "" {
		msg["reasoning_content"] = reasoning
		msg["reasoning_details"] = []interface{}{
			map[string]interface{}{
				"index": 0,
				"type":  "reasoning.text",
				"text":  reasoning,
			},
		}
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

func finalizeStreamReasoningDetails(accums map[int]*reasoningDetailAccum) ([]interface{}, string) {
	indices := make([]int, 0, len(accums))
	for idx := range accums {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	details := make([]interface{}, 0, len(indices))
	var reasoning strings.Builder
	for _, idx := range indices {
		acc := accums[idx]
		if acc == nil {
			continue
		}
		typ := acc.typ
		if typ == "" {
			typ = "reasoning.text"
		}
		detail := map[string]interface{}{
			"index": idx,
			"type":  typ,
		}
		if acc.id != "" {
			detail["id"] = acc.id
		}
		if acc.hasText {
			text := acc.text.String()
			detail["text"] = text
			if reasoning.Len() > 0 {
				reasoning.WriteString("\n")
			}
			reasoning.WriteString(text)
		}
		if acc.hasSum {
			summary := acc.summary.String()
			detail["summary"] = summary
			if reasoning.Len() > 0 {
				reasoning.WriteString("\n")
			}
			reasoning.WriteString(summary)
		}
		if acc.signature != "" {
			detail["signature"] = acc.signature
		}
		if acc.data != "" {
			detail["data"] = acc.data
		}
		if acc.encrypted != "" {
			detail["encrypted_content"] = acc.encrypted
		}
		if len(detail) > 2 {
			details = append(details, detail)
		}
	}
	return details, reasoning.String()
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
