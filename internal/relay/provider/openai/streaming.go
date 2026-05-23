package openai

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

type responsesReverseState struct {
	id             string
	model          string
	outputID       string
	contentID      string
	created        bool
	itemAdded      bool
	contentOpen    bool
	completed      bool
	finishing      bool
	finishReason   string
	seq            int
	createdAt      int64
	text           strings.Builder
	toolCalls      map[int]*responseToolCall
	toolOrder      []int
	toolItemsAdded map[int]bool
}

type responseToolCall struct {
	ID        string
	ItemID    string
	Name      string
	Arguments strings.Builder
}

func NewResponsesReverseStreamConverter() func([]byte) []byte {
	state := &responsesReverseState{
		id:             "resp_" + provider.RandomHex(24),
		outputID:       "msg_" + provider.RandomHex(24),
		contentID:      "ct_" + provider.RandomHex(24),
		toolCalls:      make(map[int]*responseToolCall),
		toolItemsAdded: make(map[int]bool),
	}
	return state.convertLine
}

func (s *responsesReverseState) convertLine(line []byte) []byte {
	lineStr := strings.TrimSpace(string(line))
	if !strings.HasPrefix(lineStr, "data: ") {
		return nil
	}
	data := strings.TrimPrefix(lineStr, "data: ")
	if data == "[DONE]" {
		if s.finishing && !s.completed {
			return s.complete(nil)
		}
		return nil
	}

	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Index        int                    `json:"index"`
			Delta        map[string]interface{} `json:"delta"`
			FinishReason *string                `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return s.responseFailure("openai chat stream event is not valid JSON")
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Created > 0 {
		s.createdAt = chunk.Created
	}

	var out []byte
	if !s.created {
		s.created = true
		out = append(out, s.responseEvent("response.created", s.responseEnvelope("in_progress", nil))...)
		out = append(out, s.responseEvent("response.in_progress", s.responseEnvelope("in_progress", nil))...)
	}

	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil {
			out = append(out, s.complete(chunk.Usage)...)
		}
		return out
	}

	choice := chunk.Choices[0]
	if hasOpenAIReasoningDelta(choice.Delta) {
		return s.responseFailure("openai chat stream reasoning deltas cannot be converted to responses stream")
	}
	text := openAITextDelta(choice.Delta)
	if text != "" {
		s.text.WriteString(text)
		out = append(out, s.ensureOutputStarted()...)
		out = append(out, s.event("response.output_text.delta", map[string]interface{}{
			"type":            "response.output_text.delta",
			"sequence_number": s.nextSeq(),
			"item_id":         s.outputID,
			"output_index":    0,
			"content_index":   0,
			"delta":           text,
			"logprobs":        []interface{}{},
			"obfuscation":     "",
			"content_part_id": s.contentID,
			"response_id":     s.id,
			"model":           s.model,
		})...)
	}

	if tcs, ok := choice.Delta["tool_calls"].([]interface{}); ok {
		for _, raw := range tcs {
			tc, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			out = append(out, s.handleToolCallDelta(tc)...)
		}
	}

	if choice.FinishReason != nil {
		s.finishing = true
		s.finishReason = *choice.FinishReason
		if openAIStreamUsageHasTokens(chunk.Usage) {
			out = append(out, s.complete(chunk.Usage)...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *responsesReverseState) handleToolCallDelta(delta map[string]interface{}) []byte {
	idx := provider.ToInt(delta["index"])
	tc := s.toolCalls[idx]
	if tc == nil {
		id, _ := delta["id"].(string)
		if id == "" {
			id = "call_" + provider.RandomHex(12)
		}
		tc = &responseToolCall{ID: id, ItemID: "fc_" + provider.RandomHex(12)}
		s.toolCalls[idx] = tc
		s.toolOrder = append(s.toolOrder, idx)
	}
	fn, _ := delta["function"].(map[string]interface{})
	if name, _ := fn["name"].(string); name != "" {
		tc.Name = name
	}
	if args, _ := fn["arguments"].(string); args != "" {
		tc.Arguments.WriteString(args)
	}

	var out []byte
	if !s.toolItemsAdded[idx] && tc.Name != "" {
		s.toolItemsAdded[idx] = true
		out = append(out, s.event("response.output_item.added", map[string]interface{}{
			"type":         "response.output_item.added",
			"output_index": s.toolOutputIndex(idx),
			"item": map[string]interface{}{
				"id":        tc.ItemID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": "",
			},
			"response_id": s.id,
		})...)
	}
	if args, _ := fn["arguments"].(string); args != "" {
		out = append(out, s.event("response.function_call_arguments.delta", map[string]interface{}{
			"type":         "response.function_call_arguments.delta",
			"item_id":      tc.ItemID,
			"call_id":      tc.ID,
			"output_index": s.toolOutputIndex(idx),
			"delta":        args,
			"response_id":  s.id,
		})...)
	}
	return out
}

func openAIStreamUsageHasTokens(usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}) bool {
	return usage != nil && (usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.TotalTokens > 0)
}

func (s *responsesReverseState) ensureOutputStarted() []byte {
	var out []byte
	if !s.itemAdded {
		s.itemAdded = true
		out = append(out, s.event("response.output_item.added", map[string]interface{}{
			"type":            "response.output_item.added",
			"sequence_number": s.nextSeq(),
			"output_index":    0,
			"item": map[string]interface{}{
				"id":      s.outputID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []interface{}{},
			},
		})...)
	}
	if !s.contentOpen {
		s.contentOpen = true
		out = append(out, s.event("response.content_part.added", map[string]interface{}{
			"type":            "response.content_part.added",
			"sequence_number": s.nextSeq(),
			"item_id":         s.outputID,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        "",
				"annotations": []interface{}{},
			},
		})...)
	}
	return out
}

func (s *responsesReverseState) complete(usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}) []byte {
	if s.completed {
		return nil
	}
	s.completed = true
	var out []byte
	if s.text.Len() > 0 || len(s.toolCalls) == 0 {
		out = append(out, s.ensureOutputStarted()...)
	}
	if s.contentOpen {
		s.contentOpen = false
		out = append(out, s.event("response.output_text.done", map[string]interface{}{
			"type":            "response.output_text.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.outputID,
			"output_index":    0,
			"content_index":   0,
			"text":            s.text.String(),
			"annotations":     []interface{}{},
		})...)
		out = append(out, s.event("response.content_part.done", map[string]interface{}{
			"type":            "response.content_part.done",
			"sequence_number": s.nextSeq(),
			"item_id":         s.outputID,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        s.text.String(),
				"annotations": []interface{}{},
			},
		})...)
	}
	if s.itemAdded {
		out = append(out, s.event("response.output_item.done", map[string]interface{}{
			"type":            "response.output_item.done",
			"sequence_number": s.nextSeq(),
			"output_index":    0,
			"item": map[string]interface{}{
				"id":     s.outputID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type":        "output_text",
						"text":        s.text.String(),
						"annotations": []interface{}{},
					},
				},
			},
		})...)
	}
	for _, idx := range s.toolOrder {
		tc := s.toolCalls[idx]
		if tc == nil {
			continue
		}
		args := tc.Arguments.String()
		out = append(out, s.event("response.function_call_arguments.done", map[string]interface{}{
			"type":         "response.function_call_arguments.done",
			"item_id":      tc.ItemID,
			"call_id":      tc.ID,
			"name":         tc.Name,
			"output_index": s.toolOutputIndex(idx),
			"arguments":    args,
			"response_id":  s.id,
		})...)
		out = append(out, s.event("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": s.toolOutputIndex(idx),
			"response_id":  s.id,
			"item": map[string]interface{}{
				"id":        tc.ItemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": args,
			},
		})...)
	}
	status := "completed"
	event := "response.completed"
	if responsesFinishReasonIncomplete(s.finishReason) {
		status = "incomplete"
		event = "response.incomplete"
	}
	out = append(out, s.responseEvent(event, s.responseEnvelope(status, usage))...)
	return out
}

func (s *responsesReverseState) responseEnvelope(status string, usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}) map[string]interface{} {
	env := map[string]interface{}{
		"id":                  s.id,
		"object":              "response",
		"created_at":          s.responseCreatedAt(),
		"status":              status,
		"model":               s.model,
		"output":              s.responseOutput(status),
		"parallel_tool_calls": true,
	}
	if status == "incomplete" {
		env["incomplete_details"] = map[string]interface{}{"reason": responsesIncompleteReason(s.finishReason)}
	}
	if usage != nil {
		total := usage.TotalTokens
		if total == 0 {
			total = usage.PromptTokens + usage.CompletionTokens
		}
		env["usage"] = map[string]interface{}{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
			"total_tokens":  total,
		}
	}
	return env
}

func (s *responsesReverseState) responseCreatedAt() int64 {
	if s.createdAt > 0 {
		return s.createdAt
	}
	return time.Now().Unix()
}

func (s *responsesReverseState) responseOutput(status string) []interface{} {
	itemStatus := status
	output := []interface{}{
		map[string]interface{}{
			"id":     s.outputID,
			"type":   "message",
			"status": itemStatus,
			"role":   "assistant",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "output_text",
					"text":        s.text.String(),
					"annotations": []interface{}{},
				},
			},
		},
	}
	if s.text.Len() == 0 && len(s.toolCalls) > 0 {
		output = []interface{}{}
	}
	for _, idx := range s.toolOrder {
		tc := s.toolCalls[idx]
		if tc == nil {
			continue
		}
		output = append(output, map[string]interface{}{
			"id":        tc.ItemID,
			"type":      "function_call",
			"status":    itemStatus,
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": tc.Arguments.String(),
		})
	}
	return output
}

func responsesFinishReasonIncomplete(reason string) bool {
	return reason == "length" || reason == "content_filter"
}

func responsesIncompleteReason(reason string) string {
	if reason == "content_filter" {
		return "content_filter"
	}
	return "max_output_tokens"
}

func responsesIncompleteFinishReason(v interface{}) string {
	source, _ := v.(map[string]interface{})
	if resp, ok := source["response"].(map[string]interface{}); ok {
		source = resp
	}
	details, _ := source["incomplete_details"].(map[string]interface{})
	reason, _ := details["reason"].(string)
	if reason == "content_filter" {
		return "content_filter"
	}
	return "length"
}

func (s *responsesReverseState) toolOutputIndex(idx int) int {
	if s.text.Len() > 0 || s.itemAdded {
		return idx + 1
	}
	return idx
}

func (s *responsesReverseState) event(name string, payload map[string]interface{}) []byte {
	payload["type"] = name
	if _, ok := payload["sequence_number"]; !ok {
		payload["sequence_number"] = s.nextSeq()
	}
	b, _ := json.Marshal(payload)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", name, b))
}

func (s *responsesReverseState) responseEvent(name string, response map[string]interface{}) []byte {
	return s.event(name, map[string]interface{}{
		"response": response,
	})
}

func (s *responsesReverseState) responseFailure(message string) []byte {
	s.completed = true
	resp := s.responseEnvelope("failed", nil)
	resp["error"] = map[string]interface{}{
		"message": message,
		"type":    "invalid_response_error",
	}
	return s.responseEvent("response.failed", resp)
}

func (s *responsesReverseState) nextSeq() int {
	s.seq++
	return s.seq
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

type responsesToChatState struct {
	event         string
	id            string
	model         string
	created       int64
	role          bool
	textSeen      bool
	hasToolCall   bool
	toolIndex     map[string]int
	toolNames     map[string]string
	itemCallID    map[string]string
	toolArgSeen   map[string]bool
	nextToolIndex int
}

func NewResponsesToChatStreamConverter() func([]byte) []byte {
	return (&responsesToChatState{toolIndex: make(map[string]int), toolNames: make(map[string]string), itemCallID: make(map[string]string), toolArgSeen: make(map[string]bool)}).convertLine
}

func NewChatStreamNormalizer() func([]byte) []byte {
	return func(line []byte) []byte {
		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" || !strings.HasPrefix(lineStr, "data: ") {
			return nil
		}
		return []byte(lineStr + "\n\n")
	}
}

func (s *responsesToChatState) convertLine(line []byte) []byte {
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" {
		return nil
	}
	if strings.HasPrefix(lineStr, "event:") {
		lines := strings.Split(lineStr, "\n")
		s.event = strings.TrimSpace(strings.TrimPrefix(lines[0], "event:"))
		for _, candidate := range lines[1:] {
			candidate = strings.TrimSpace(candidate)
			if strings.HasPrefix(candidate, "data:") {
				lineStr = candidate
				break
			}
		}
		if strings.HasPrefix(lineStr, "event:") {
			return nil
		}
	}
	if !strings.HasPrefix(lineStr, "data:") {
		return nil
	}
	data := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
	if data == "[DONE]" {
		return []byte("data: [DONE]\n\n")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return buildResponsesChatErrorChunk(s.id, s.model, "openai responses stream event is not valid JSON")
	}
	eventType, _ := payload["type"].(string)
	if eventType == "" {
		eventType = s.event
	}
	if resp, ok := payload["response"].(map[string]interface{}); ok {
		if id, ok := resp["id"].(string); ok && id != "" {
			s.id = id
		}
		if model, ok := resp["model"].(string); ok && model != "" {
			s.model = model
		}
		if createdAt := provider.ToInt(resp["created_at"]); createdAt > 0 {
			s.created = int64(createdAt)
		}
	}
	if id, ok := payload["response_id"].(string); ok && id != "" {
		s.id = id
	}
	if id, ok := payload["id"].(string); ok && id != "" && strings.HasPrefix(id, "resp_") {
		s.id = id
	}
	if model, ok := payload["model"].(string); ok && model != "" {
		s.model = model
	}
	if s.id == "" {
		s.id = "chatcmpl-" + provider.RandomHex(12)
	}

	switch eventType {
	case "response.created", "response.in_progress":
		if s.role {
			return nil
		}
		s.role = true
		return s.buildChatChunk(map[string]interface{}{"role": "assistant"}, nil, nil)
	case "response.output_text.delta":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		if !s.role {
			s.role = true
		}
		text, _ := payload["delta"].(string)
		if text == "" {
			return nil
		}
		s.textSeen = true
		return s.buildChatChunk(map[string]interface{}{"content": text}, nil, nil)
	case "response.output_item.added":
		item, _ := payload["item"].(map[string]interface{})
		typ, _ := item["type"].(string)
		if typ == "message" {
			return nil
		}
		if typ != "function_call" {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{typ})
			return nil
		}
		itemID, _ := item["call_id"].(string)
		callID := itemID
		if itemID == "" {
			itemID, _ = item["id"].(string)
			callID = itemID
		}
		if itemID == "" {
			return nil
		}
		idx, ok := s.toolIndex[itemID]
		if !ok {
			idx = s.nextToolIndex
			s.nextToolIndex++
			s.toolIndex[itemID] = idx
		}
		if rawItemID, _ := item["id"].(string); rawItemID != "" {
			s.itemCallID[rawItemID] = callID
			s.toolIndex[rawItemID] = idx
		}
		name, _ := item["name"].(string)
		s.toolNames[itemID] = name
		s.hasToolCall = true
		tc := map[string]interface{}{
			"index": idx,
			"id":    itemID,
			"type":  "function",
			"function": map[string]interface{}{
				"name":      name,
				"arguments": "",
			},
		}
		return s.buildChatChunk(map[string]interface{}{"tool_calls": []interface{}{tc}}, nil, nil)
	case "response.function_call_arguments.delta":
		itemID, _ := payload["item_id"].(string)
		if callID := s.itemCallID[itemID]; callID != "" {
			itemID = callID
		}
		idx := s.toolIndex[itemID]
		delta, _ := payload["delta"].(string)
		if delta != "" {
			s.toolArgSeen[itemID] = true
		}
		tc := map[string]interface{}{
			"index": idx,
			"function": map[string]interface{}{
				"arguments": delta,
			},
		}
		return s.buildChatChunk(map[string]interface{}{"tool_calls": []interface{}{tc}}, nil, nil)
	case "response.output_text.done", "response.content_part.done":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		if !s.textSeen {
			if text, _ := payload["text"].(string); text != "" {
				s.textSeen = true
				return s.buildChatChunk(map[string]interface{}{"content": text}, nil, nil)
			}
		}
		return nil
	case "response.function_call_arguments.done":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		return s.buildFinalToolArgumentsChunk(payload, nil)
	case "response.output_item.done":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		item, _ := payload["item"].(map[string]interface{})
		return s.buildFinalToolArgumentsChunk(payload, item)
	case "response.content_part.added":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		return nil
	case "response.completed":
		if responsesStreamPayloadHasAnnotations(payload) {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{"annotations"})
		}
		usage := openAIUsageFromResponsesPayload(payload)
		finish := "stop"
		if s.hasToolCall {
			finish = "tool_calls"
		}
		return s.buildChatChunk(map[string]interface{}{}, &finish, usage)
	case "response.incomplete":
		usage := openAIUsageFromResponsesPayload(payload)
		finish := responsesIncompleteFinishReason(payload["response"])
		return s.buildChatChunk(map[string]interface{}{}, &finish, usage)
	case "response.failed", "error":
		return buildResponsesChatErrorChunk(s.id, s.model, responseStreamErrorMessage(payload))
	default:
		if strings.Contains(eventType, "error") || strings.Contains(eventType, "failed") {
			return buildResponsesChatErrorChunk(s.id, s.model, responseStreamErrorMessage(payload))
		}
		if strings.HasPrefix(eventType, "response.") {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), []string{eventType})
			return nil
		}
		return nil
	}
}

func (s *responsesToChatState) buildFinalToolArgumentsChunk(payload map[string]interface{}, item map[string]interface{}) []byte {
	itemID, _ := payload["item_id"].(string)
	if itemID == "" && item != nil {
		itemID, _ = item["id"].(string)
	}
	callID := itemID
	if item != nil {
		if id, _ := item["call_id"].(string); id != "" {
			callID = id
		}
	}
	if mapped := s.itemCallID[itemID]; mapped != "" {
		callID = mapped
	}
	if callID == "" {
		callID, _ = payload["call_id"].(string)
	}
	if callID == "" {
		return nil
	}
	arguments, _ := payload["arguments"].(string)
	if arguments == "" && item != nil {
		arguments, _ = item["arguments"].(string)
	}
	if arguments == "" || s.toolArgSeen[callID] || s.toolArgSeen[itemID] {
		return nil
	}
	idx, ok := s.toolIndex[callID]
	if !ok {
		idx = s.toolIndex[itemID]
	}
	tc := map[string]interface{}{
		"index": idx,
		"function": map[string]interface{}{
			"arguments": arguments,
		},
	}
	s.toolArgSeen[callID] = true
	return s.buildChatChunk(map[string]interface{}{"tool_calls": []interface{}{tc}}, nil, nil)
}

func responsesStreamPayloadHasAnnotations(payload map[string]interface{}) bool {
	if annotations, ok := payload["annotations"].([]interface{}); ok && len(annotations) > 0 {
		return true
	}
	if part, ok := payload["part"].(map[string]interface{}); ok {
		if annotations, ok := part["annotations"].([]interface{}); ok && len(annotations) > 0 {
			return true
		}
	}
	if content, ok := payload["content"].(map[string]interface{}); ok {
		if annotations, ok := content["annotations"].([]interface{}); ok && len(annotations) > 0 {
			return true
		}
	}
	if resp, ok := payload["response"].(map[string]interface{}); ok {
		if output, ok := resp["output"].([]interface{}); ok {
			for _, itemRaw := range output {
				item, _ := itemRaw.(map[string]interface{})
				content, _ := item["content"].([]interface{})
				for _, partRaw := range content {
					part, _ := partRaw.(map[string]interface{})
					if annotations, ok := part["annotations"].([]interface{}); ok && len(annotations) > 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

func responseStreamErrorMessage(payload map[string]interface{}) string {
	if errObj, ok := payload["error"].(map[string]interface{}); ok {
		if msg, _ := errObj["message"].(string); msg != "" {
			return msg
		}
	}
	if resp, ok := payload["response"].(map[string]interface{}); ok {
		if errObj, ok := resp["error"].(map[string]interface{}); ok {
			if msg, _ := errObj["message"].(string); msg != "" {
				return msg
			}
		}
	}
	if msg, _ := payload["message"].(string); msg != "" {
		return msg
	}
	return "openai responses stream error"
}

func buildResponsesChatErrorChunk(id, model, message string) []byte {
	if id == "" {
		id = "chatcmpl-" + provider.RandomHex(12)
	}
	payload := map[string]interface{}{
		"id":     id,
		"object": "error",
		"model":  model,
		"error": map[string]interface{}{
			"message": message,
			"type":    "upstream_error",
		},
	}
	b, _ := json.Marshal(payload)
	return []byte("data: " + string(b) + "\n\n")
}

func (s *responsesToChatState) buildChatChunk(delta map[string]interface{}, finish *string, usage map[string]interface{}) []byte {
	created := s.created
	if created == 0 {
		created = time.Now().Unix()
	}
	return buildResponsesChatChunk(s.id, s.model, created, delta, finish, usage)
}

func openAIUsageFromResponsesPayload(payload map[string]interface{}) map[string]interface{} {
	source := payload
	if resp, ok := payload["response"].(map[string]interface{}); ok {
		source = resp
	}
	usage, ok := source["usage"].(map[string]interface{})
	if !ok {
		return nil
	}
	pt := provider.ToInt(usage["input_tokens"])
	ct := provider.ToInt(usage["output_tokens"])
	if pt == 0 && ct == 0 {
		pt = provider.ToInt(usage["prompt_tokens"])
		ct = provider.ToInt(usage["completion_tokens"])
	}
	return map[string]interface{}{
		"prompt_tokens":     pt,
		"completion_tokens": ct,
		"total_tokens":      pt + ct,
	}
}

func buildResponsesChatChunk(id, model string, created int64, delta map[string]interface{}, finish *string, usage map[string]interface{}) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			},
		},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, _ := json.Marshal(chunk)
	return []byte("data: " + string(b) + "\n\n")
}
