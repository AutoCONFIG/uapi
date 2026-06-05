package relay

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
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
		} else if reasoning, ok := delta["reasoning"].(string); ok {
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

func StreamToNonStreamForFormat(upstreamFormat provider.Format, sseBody []byte) ([]byte, bool, provider.Format) {
	switch upstreamFormat {
	case provider.FormatOpenAIResponses, provider.FormatCodexResponses:
		body, complete := ResponsesStreamToNonStreamChecked(sseBody)
		return body, complete, upstreamFormat
	default:
		body, complete := StreamToNonStreamChecked(sseBody)
		return body, complete, provider.FormatOpenAIChatCompletions
	}
}

type responsesStreamAccum struct {
	id        string
	model     string
	createdAt int64
	status    string
	output    map[int]map[string]interface{}
	order     []int
	text      map[int]string
	toolArgs  map[int]string
	usage     map[string]interface{}
	err       map[string]interface{}
	complete  bool
}

func ResponsesStreamToNonStreamChecked(sseBody []byte) ([]byte, bool) {
	acc := responsesStreamAccum{
		output:   map[int]map[string]interface{}{},
		text:     map[int]string{},
		toolArgs: map[int]string{},
	}
	sseBody = normalizeSSEBufferForConversion(sseBody)
	for _, event := range splitSSEEvents(sseBody) {
		for _, data := range sseDataPayloads(event) {
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(data), &raw); err != nil {
				continue
			}
			acc.applyResponsesStreamEvent(raw)
		}
	}
	if acc.err != nil {
		body, _ := json.Marshal(map[string]interface{}{"object": "error", "error": acc.err})
		return body, true
	}
	body, _ := json.Marshal(acc.responsesBody())
	return body, acc.complete
}

func (a *responsesStreamAccum) applyResponsesStreamEvent(raw map[string]interface{}) {
	typ, _ := raw["type"].(string)
	switch typ {
	case "error":
		a.err = responseErrorMap(raw["error"])
	case "response.failed":
		if response, _ := raw["response"].(map[string]interface{}); response != nil {
			a.applyResponseMeta(response)
			a.err = responseErrorMap(response["error"])
			return
		}
		a.err = responseErrorMap(raw["error"])
	case "response.created", "response.in_progress":
		if response, _ := raw["response"].(map[string]interface{}); response != nil {
			a.applyResponseMeta(response)
		}
	case "response.output_item.added", "response.output_item.done":
		idx := intField(raw, "output_index")
		if item, _ := raw["item"].(map[string]interface{}); item != nil {
			a.rememberOutputItem(idx, cloneMap(item))
		}
	case "response.output_text.delta":
		idx := intField(raw, "output_index")
		if delta, _ := raw["delta"].(string); delta != "" {
			a.text[idx] += delta
		}
	case "response.output_text.done":
		idx := intField(raw, "output_index")
		if text, _ := raw["text"].(string); text != "" {
			a.text[idx] = text
		}
	case "response.content_part.done":
		idx := intField(raw, "output_index")
		if part, _ := raw["part"].(map[string]interface{}); part != nil {
			if text, _ := part["text"].(string); text != "" {
				a.text[idx] = text
			}
		}
	case "response.function_call_arguments.delta":
		idx := intField(raw, "output_index")
		if args, _ := raw["arguments"].(string); args != "" {
			a.toolArgs[idx] += args
		} else if delta, _ := raw["delta"].(string); delta != "" {
			a.toolArgs[idx] += delta
		} else if deltaMap, _ := raw["delta"].(map[string]interface{}); deltaMap != nil {
			if args, _ := deltaMap["arguments"].(string); args != "" {
				a.toolArgs[idx] += args
			}
		}
	case "response.function_call_arguments.done":
		idx := intField(raw, "output_index")
		if args, _ := raw["arguments"].(string); args != "" {
			a.toolArgs[idx] = args
		}
	case "response.completed":
		a.complete = true
		if response, _ := raw["response"].(map[string]interface{}); response != nil {
			a.applyResponseMeta(response)
			if output, _ := response["output"].([]interface{}); len(output) > 0 {
				a.output = map[int]map[string]interface{}{}
				a.order = nil
				for idx, itemRaw := range output {
					if item, _ := itemRaw.(map[string]interface{}); item != nil {
						a.rememberOutputItem(idx, cloneMap(item))
					}
				}
			}
		}
	case "response.incomplete":
		a.complete = true
		a.status = "incomplete"
		if response, _ := raw["response"].(map[string]interface{}); response != nil {
			a.applyResponseMeta(response)
		}
	}
	if usage, _ := raw["usage"].(map[string]interface{}); usage != nil {
		a.usage = cloneMap(usage)
	}
}

func (a *responsesStreamAccum) applyResponseMeta(response map[string]interface{}) {
	if id, _ := response["id"].(string); id != "" {
		a.id = id
	}
	if model, _ := response["model"].(string); model != "" {
		a.model = model
	}
	if status, _ := response["status"].(string); status != "" {
		a.status = status
	}
	if created := int64Field(response, "created_at"); created > 0 {
		a.createdAt = created
	}
	if usage, _ := response["usage"].(map[string]interface{}); usage != nil {
		a.usage = cloneMap(usage)
	}
}

func (a *responsesStreamAccum) rememberOutputItem(index int, item map[string]interface{}) {
	if _, ok := a.output[index]; !ok {
		a.order = append(a.order, index)
	}
	a.output[index] = item
}

func (a *responsesStreamAccum) responsesBody() map[string]interface{} {
	sort.Ints(a.order)
	output := make([]interface{}, 0, len(a.order))
	for _, idx := range a.order {
		item := cloneMap(a.output[idx])
		if typ, _ := item["type"].(string); typ == "message" {
			a.fillResponsesMessageText(item, idx)
		}
		if typ, _ := item["type"].(string); typ == "function_call" {
			if args := a.toolArgs[idx]; args != "" {
				item["arguments"] = args
			}
		}
		output = append(output, item)
	}
	status := a.status
	if status == "" {
		status = "completed"
	}
	if a.createdAt == 0 {
		a.createdAt = time.Now().Unix()
	}
	body := map[string]interface{}{
		"id":         a.id,
		"object":     "response",
		"created_at": a.createdAt,
		"model":      a.model,
		"status":     status,
		"output":     output,
	}
	if a.usage != nil {
		body["usage"] = a.usage
	}
	return body
}

func (a *responsesStreamAccum) fillResponsesMessageText(item map[string]interface{}, index int) {
	text := a.text[index]
	if text == "" {
		return
	}
	content, _ := item["content"].([]interface{})
	if len(content) == 0 {
		item["content"] = []interface{}{map[string]interface{}{"type": "output_text", "text": text, "annotations": []interface{}{}}}
		return
	}
	first, _ := content[0].(map[string]interface{})
	if first != nil {
		first["text"] = text
	}
}

func responseErrorMap(raw interface{}) map[string]interface{} {
	if errMap, _ := raw.(map[string]interface{}); errMap != nil {
		return cloneMap(errMap)
	}
	return map[string]interface{}{"type": "upstream_error", "message": "upstream stream failed"}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func intField(obj map[string]interface{}, key string) int {
	return int(int64Field(obj, key))
}

func int64Field(obj map[string]interface{}, key string) int64 {
	switch v := obj[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
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
