package stream

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

type chatIRParser struct {
	id       string
	model    string
	created  int64
	started  bool
	finished bool
}

func newChatIRParser() streamIRParser { return &chatIRParser{} }

func (p *chatIRParser) Parse(line []byte) []relayir.StreamEvent {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	var event struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int             `json:"index"`
			Delta        json.RawMessage `json:"delta"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]interface{} `json:"usage,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil || len(event.Choices) == 0 {
		return nil
	}
	if p.id == "" && event.ID != "" {
		p.id = event.ID
		p.model = event.Model
		p.created = event.Created
	}

	var out []relayir.StreamEvent
	if !p.started && p.id != "" {
		p.started = true
		out = append(out, relayir.StreamEvent{
			Type:       relayir.EventResponseCreated,
			ResponseID: p.id,
			Model:      p.model,
			Native: relayir.NativeEnvelope{
				Protocol: relayir.ProtocolOpenAIChat,
				Meta:     rawMeta("created", p.created),
			},
		})
	}

	choice := event.Choices[0]
	var delta struct {
		Role             string          `json:"role"`
		Content          string          `json:"content"`
		ReasoningContent string          `json:"reasoning_content"`
		Reasoning        string          `json:"reasoning"`
		ReasoningDetails json.RawMessage `json:"reasoning_details"`
		ToolCalls        []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	_ = json.Unmarshal(choice.Delta, &delta)

	if delta.Role != "" {
		out = append(out, relayir.StreamEvent{Type: relayir.EventMessageStart, ResponseID: p.id, Model: p.model, ChoiceIndex: choice.Index})
	}
	if delta.Content != "" {
		out = append(out, relayir.StreamEvent{Type: relayir.EventContentDelta, ResponseID: p.id, Model: p.model, ChoiceIndex: choice.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: delta.Content}})
	}
	out = append(out, p.reasoningEvents(choice.Index, delta)...)
	for _, tc := range delta.ToolCalls {
		meta := map[string]json.RawMessage{}
		if tc.ID != "" {
			meta["call_id"] = rawStringValue(tc.ID)
		}
		if tc.Function.Name != "" {
			meta["name"] = rawStringValue(tc.Function.Name)
		}
		if tc.Type != "" {
			meta["type"] = rawStringValue(tc.Type)
		}
		if tc.Function.Name != "" && tc.Function.Arguments == "" {
			out = append(out, relayir.StreamEvent{
				Type:        relayir.EventToolCallStart,
				ResponseID:  p.id,
				Model:       p.model,
				ChoiceIndex: choice.Index,
				ItemIndex:   tc.Index,
				Delta:       relayir.ItemDelta{Kind: relayir.ItemToolUse},
				Native:      relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIChat, Meta: meta},
			})
		}
		if tc.Function.Arguments != "" {
			out = append(out, relayir.StreamEvent{
				Type:        relayir.EventToolArgDelta,
				ResponseID:  p.id,
				Model:       p.model,
				ChoiceIndex: choice.Index,
				ItemIndex:   tc.Index,
				Delta:       relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: tc.Function.Arguments},
				Native:      relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIChat, Meta: meta},
			})
		}
	}
	if choice.FinishReason != "" {
		p.finished = true
		out = append(out, relayir.StreamEvent{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, ChoiceIndex: choice.Index, Finish: &relayir.Finish{Reason: chatFinishToIR(choice.FinishReason), NativeReason: choice.FinishReason}, Usage: chatUsageToIR(event.Usage)})
	}
	return out
}

func (p *chatIRParser) reasoningEvents(choiceIndex int, delta struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ReasoningDetails json.RawMessage `json:"reasoning_details"`
	ToolCalls        []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}) []relayir.StreamEvent {
	var out []relayir.StreamEvent
	for _, detail := range parseChatReasoningDetails(delta.ReasoningDetails) {
		if encrypted := reasoningDetailEncrypted(detail); encrypted != "" {
			out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ChoiceIndex: choiceIndex, ItemIndex: detail.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: encrypted}})
		}
		if text := reasoningDetailText(detail); text != "" {
			out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ChoiceIndex: choiceIndex, ItemIndex: detail.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: text, Signature: reasoningDetailSignature(detail)}})
		}
	}
	text := delta.ReasoningContent
	if text == "" {
		text = delta.Reasoning
	}
	if text != "" {
		out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ChoiceIndex: choiceIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: text}})
	}
	return out
}

func (p *chatIRParser) Done() []relayir.StreamEvent {
	if p.finished || p.id == "" {
		return nil
	}
	p.finished = true
	return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: relayir.FinishStop, NativeReason: "stop"}}}
}

func (p *chatIRParser) Reset() { *p = chatIRParser{} }

type responsesIRParser struct {
	id       string
	model    string
	finished bool
}

func newResponsesIRParser() streamIRParser { return &responsesIRParser{} }

func (p *responsesIRParser) Parse(line []byte) []relayir.StreamEvent {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	var event struct {
		Type        string          `json:"type"`
		Delta       json.RawMessage `json:"delta,omitempty"`
		Item        json.RawMessage `json:"item,omitempty"`
		ItemID      string          `json:"item_id,omitempty"`
		OutputIndex int             `json:"output_index,omitempty"`
		Response    json.RawMessage `json:"response,omitempty"`
		Text        string          `json:"text,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	switch event.Type {
	case "response.created":
		var meta struct {
			ID       string `json:"id"`
			Model    string `json:"model"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		_ = json.Unmarshal([]byte(data), &meta)
		if meta.ID == "" {
			meta.ID = meta.Response.ID
		}
		if meta.Model == "" {
			meta.Model = meta.Response.Model
		}
		p.id, p.model = meta.ID, meta.Model
		return []relayir.StreamEvent{{Type: relayir.EventResponseCreated, ResponseID: p.id, Model: p.model}}
	case "response.output_text.delta":
		return []relayir.StreamEvent{{Type: relayir.EventContentDelta, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: responsesTextDelta(event.Delta)}}}
	case "response.output_text.done":
		text := event.Text
		if text == "" {
			text = responsesTextDelta(event.Delta)
		}
		return []relayir.StreamEvent{{Type: relayir.EventContentPartEnd, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: text}}}
	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		return []relayir.StreamEvent{{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: responsesTextDelta(event.Delta)}}}
	case "response.reasoning.done", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		text := event.Text
		if text == "" {
			text = responsesTextDelta(event.Delta)
		}
		return []relayir.StreamEvent{{Type: relayir.EventReasoningEnd, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: text}}}
	case "response.output_item.added":
		return p.outputItemAdded(event.Item, event.OutputIndex)
	case "response.output_item.done":
		return p.outputItemDone(event.Item, event.OutputIndex)
	case "response.function_call_arguments.delta":
		var delta struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		_ = json.Unmarshal(event.Delta, &delta)
		if delta.Arguments == "" {
			delta.Arguments = responsesTextDelta(event.Delta)
		}
		if delta.CallID == "" {
			delta.CallID = event.ItemID
		}
		return []relayir.StreamEvent{{Type: relayir.EventToolArgDelta, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: delta.Arguments}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: rawMeta("call_id", delta.CallID)}}}
	case "response.completed":
		p.finished = true
		return p.completed(event.Response, relayir.FinishStop, "stop")
	case "response.incomplete":
		p.finished = true
		return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: relayir.FinishMaxTokens, NativeReason: "length"}}}
	}
	return nil
}

func (p *responsesIRParser) outputItemAdded(raw json.RawMessage, index int) []relayir.StreamEvent {
	var item struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		CallID           string `json:"call_id"`
		Name             string `json:"name"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	switch item.Type {
	case "function_call":
		return []relayir.StreamEvent{{Type: relayir.EventToolCallStart, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: rawMeta("call_id", item.CallID, "name", item.Name)}}}
	case "reasoning":
		if item.EncryptedContent != "" {
			return []relayir.StreamEvent{{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: item.EncryptedContent}}}
		}
	}
	return nil
}

func (p *responsesIRParser) outputItemDone(raw json.RawMessage, index int) []relayir.StreamEvent {
	var item struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.Type != "reasoning" || item.EncryptedContent == "" {
		return nil
	}
	return []relayir.StreamEvent{{Type: relayir.EventReasoningEnd, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: item.EncryptedContent}}}
}

func (p *responsesIRParser) completed(raw json.RawMessage, reason relayir.FinishReason, nativeReason string) []relayir.StreamEvent {
	var completed struct {
		ID     string                 `json:"id"`
		Model  string                 `json:"model"`
		Output []responsesOutputItem  `json:"output"`
		Usage  map[string]interface{} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &completed)
	if p.id == "" {
		p.id = completed.ID
	}
	if p.model == "" {
		p.model = completed.Model
	}
	var out []relayir.StreamEvent
	for idx, item := range completed.Output {
		if item.Type == "reasoning" {
			for _, summary := range item.Summary {
				out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningEnd, ResponseID: p.id, Model: p.model, ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: summary.Text}})
			}
			if item.EncryptedContent != "" {
				out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningEnd, ResponseID: p.id, Model: p.model, ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: item.EncryptedContent}})
			}
			continue
		}
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			text := content.Text
			if text == "" {
				text = content.OutputText
			}
			out = append(out, relayir.StreamEvent{Type: relayir.EventContentPartEnd, ResponseID: p.id, Model: p.model, ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: text}})
		}
	}
	out = append(out, relayir.StreamEvent{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: reason, NativeReason: nativeReason}, Usage: responsesUsageToIR(completed.Usage)})
	return out
}

func (p *responsesIRParser) Done() []relayir.StreamEvent {
	if p.finished || p.id == "" {
		return nil
	}
	p.finished = true
	return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: relayir.FinishStop, NativeReason: "stop"}}}
}

func (p *responsesIRParser) Reset() { *p = responsesIRParser{} }

type chatIREmitter struct {
	id                   string
	model                string
	finished             bool
	textBuffer           strings.Builder
	reasoningBuffer      strings.Builder
	reasoningOpaqueStore map[string]bool
	toolCallIDToIndex    map[string]int
}

func newChatIREmitter() streamIREmitter {
	return &chatIREmitter{reasoningOpaqueStore: map[string]bool{}, toolCallIDToIndex: map[string]int{}}
}

func (e *chatIREmitter) Emit(event relayir.StreamEvent) []byte {
	e.setMeta(event)
	switch event.Type {
	case relayir.EventResponseCreated:
		return chatChunk(e.id, e.model, map[string]interface{}{"role": "assistant"}, nil, nil)
	case relayir.EventMessageStart:
		return chatChunk(e.id, e.model, map[string]interface{}{"role": "assistant"}, nil, nil)
	case relayir.EventContentDelta:
		if event.Delta.Text == "" {
			return nil
		}
		e.textBuffer.WriteString(event.Delta.Text)
		return chatChunk(e.id, e.model, map[string]interface{}{"content": event.Delta.Text}, nil, nil)
	case relayir.EventContentPartEnd:
		return e.emitMissingText(event.Delta.Text)
	case relayir.EventReasoningDelta:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return e.emitReasoningOpaque(event.Delta.Signature)
		}
		return e.emitMissingReasoning(event.Delta.Text)
	case relayir.EventReasoningEnd:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return e.emitReasoningOpaque(event.Delta.Signature)
		}
		return e.emitMissingReasoning(event.Delta.Text)
	case relayir.EventToolCallStart:
		callID := rawMetaString(event.Native.Meta, "call_id")
		name := rawMetaString(event.Native.Meta, "name")
		if callID == "" {
			callID = "call_" + strconv.Itoa(len(e.toolCallIDToIndex))
		}
		idx := len(e.toolCallIDToIndex)
		e.toolCallIDToIndex[callID] = idx
		return chatChunk(e.id, e.model, map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{"index": idx, "id": callID, "type": "function", "function": map[string]interface{}{"name": name, "arguments": ""}}}}, nil, nil)
	case relayir.EventToolArgDelta:
		callID := rawMetaString(event.Native.Meta, "call_id")
		idx := e.toolCallIDToIndex[callID]
		return chatChunk(e.id, e.model, map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{"index": idx, "id": callID, "type": "function", "function": map[string]interface{}{"arguments": event.Delta.Arguments}}}}, nil, nil)
	case relayir.EventResponseDone:
		e.finished = true
		return chatChunk(e.id, e.model, map[string]interface{}{}, irFinishToChat(event.Finish), irUsageToChat(event.Usage))
	}
	return nil
}

func (e *chatIREmitter) setMeta(event relayir.StreamEvent) {
	if e.id == "" && event.ResponseID != "" {
		e.id = event.ResponseID
	}
	if e.model == "" && event.Model != "" {
		e.model = event.Model
	}
}

func (e *chatIREmitter) emitMissingText(text string) []byte {
	if text == "" {
		return nil
	}
	current := e.textBuffer.String()
	if strings.HasPrefix(text, current) {
		missing := strings.TrimPrefix(text, current)
		if missing == "" {
			return nil
		}
		e.textBuffer.WriteString(missing)
		return chatChunk(e.id, e.model, map[string]interface{}{"content": missing}, nil, nil)
	}
	if strings.Contains(current, text) {
		return nil
	}
	e.textBuffer.WriteString(text)
	return chatChunk(e.id, e.model, map[string]interface{}{"content": text}, nil, nil)
}

func (e *chatIREmitter) emitMissingReasoning(text string) []byte {
	if text == "" {
		return nil
	}
	current := e.reasoningBuffer.String()
	if strings.HasPrefix(text, current) {
		missing := strings.TrimPrefix(text, current)
		if missing == "" {
			return nil
		}
		e.reasoningBuffer.WriteString(missing)
		return chatChunk(e.id, e.model, reasoningTextDelta(missing, 0, ""), nil, nil)
	}
	if strings.Contains(current, text) {
		return nil
	}
	e.reasoningBuffer.WriteString(text)
	return chatChunk(e.id, e.model, reasoningTextDelta(text, 0, ""), nil, nil)
}

func (e *chatIREmitter) emitReasoningOpaque(value string) []byte {
	if value == "" || e.reasoningOpaqueStore[value] {
		return nil
	}
	e.reasoningOpaqueStore[value] = true
	return chatChunk(e.id, e.model, reasoningEncryptedDelta(0, value), nil, nil)
}

func (e *chatIREmitter) Done() []byte {
	if e.finished {
		return nil
	}
	e.finished = true
	return chatChunk(e.id, e.model, map[string]interface{}{}, "stop", nil)
}

func (e *chatIREmitter) Reset() {
	*e = chatIREmitter{reasoningOpaqueStore: map[string]bool{}, toolCallIDToIndex: map[string]int{}}
}

type responsesIREmitter struct {
	inner StreamConverter
}

func newResponsesIREmitter() streamIREmitter {
	return &responsesIREmitter{inner: newChatToResponsesConverter()}
}

func (e *responsesIREmitter) Emit(event relayir.StreamEvent) []byte {
	return e.inner.Convert(chatChunkFromIR(event))
}

func (e *responsesIREmitter) Done() []byte { return e.inner.Done() }

func (e *responsesIREmitter) Reset() {
	e.inner.Reset()
	e.inner = newChatToResponsesConverter()
}

func chatChunkFromIR(event relayir.StreamEvent) []byte {
	id := event.ResponseID
	model := event.Model
	switch event.Type {
	case relayir.EventResponseCreated, relayir.EventMessageStart:
		return chatChunk(id, model, map[string]interface{}{"role": "assistant"}, nil, nil)
	case relayir.EventContentDelta:
		return chatChunk(id, model, map[string]interface{}{"content": event.Delta.Text}, nil, nil)
	case relayir.EventReasoningDelta:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return chatChunk(id, model, reasoningEncryptedDelta(event.ItemIndex, event.Delta.Signature), nil, nil)
		}
		return chatChunk(id, model, reasoningTextDelta(event.Delta.Text, event.ItemIndex, event.Delta.Signature), nil, nil)
	case relayir.EventToolCallStart:
		callID := rawMetaString(event.Native.Meta, "call_id")
		name := rawMetaString(event.Native.Meta, "name")
		return chatChunk(id, model, map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{"index": event.ItemIndex, "id": callID, "type": "function", "function": map[string]interface{}{"name": name, "arguments": ""}}}}, nil, nil)
	case relayir.EventToolArgDelta:
		callID := rawMetaString(event.Native.Meta, "call_id")
		return chatChunk(id, model, map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{"index": event.ItemIndex, "id": callID, "type": "function", "function": map[string]interface{}{"arguments": event.Delta.Arguments}}}}, nil, nil)
	case relayir.EventResponseDone:
		return chatChunk(id, model, map[string]interface{}{}, irFinishToChat(event.Finish), irUsageToChat(event.Usage))
	}
	return nil
}

func chatFinishToIR(reason string) relayir.FinishReason {
	switch reason {
	case "stop":
		return relayir.FinishStop
	case "length":
		return relayir.FinishMaxTokens
	case "tool_calls", "function_call":
		return relayir.FinishToolCall
	case "content_filter":
		return relayir.FinishContentFilter
	default:
		return relayir.FinishUnknown
	}
}

func irFinishToChat(finish *relayir.Finish) interface{} {
	if finish == nil {
		return nil
	}
	if finish.NativeReason != "" {
		return finish.NativeReason
	}
	switch finish.Reason {
	case relayir.FinishMaxTokens:
		return "length"
	case relayir.FinishToolCall:
		return "tool_calls"
	case relayir.FinishContentFilter, relayir.FinishSafety:
		return "content_filter"
	default:
		return "stop"
	}
}

func chatUsageToIR(usage map[string]interface{}) *relayir.Usage {
	if len(usage) == 0 {
		return nil
	}
	return &relayir.Usage{
		InputTokens:  numericUsageValue(usage, "prompt_tokens"),
		OutputTokens: numericUsageValue(usage, "completion_tokens"),
		TotalTokens:  numericUsageValue(usage, "total_tokens"),
	}
}

func responsesUsageToIR(usage map[string]interface{}) *relayir.Usage {
	if len(usage) == 0 {
		return nil
	}
	prompt := numericUsageValue(usage, "prompt_tokens", "input_tokens")
	completion := numericUsageValue(usage, "completion_tokens", "output_tokens")
	total := numericUsageValue(usage, "total_tokens")
	if total == 0 && (prompt != 0 || completion != 0) {
		total = prompt + completion
	}
	return &relayir.Usage{InputTokens: prompt, OutputTokens: completion, TotalTokens: total}
}

func irUsageToChat(usage *relayir.Usage) map[string]interface{} {
	if usage == nil {
		return nil
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && total == 0 {
		return nil
	}
	return map[string]interface{}{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": total}
}

func rawMeta(kv ...interface{}) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok || key == "" {
			continue
		}
		raw, _ := json.Marshal(kv[i+1])
		out[key] = raw
	}
	return out
}

func rawStringValue(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func rawMetaString(meta map[string]json.RawMessage, key string) string {
	if len(meta) == 0 {
		return ""
	}
	var out string
	_ = json.Unmarshal(meta[key], &out)
	return out
}

func init() {
	RegisterIRParser(convert.FormatOpenAIChatCompletions, newChatIRParser)
	RegisterIREmitter(convert.FormatOpenAIChatCompletions, newChatIREmitter)
	RegisterIRParser(convert.FormatOpenAIResponses, newResponsesIRParser)
	RegisterIREmitter(convert.FormatOpenAIResponses, newResponsesIREmitter)
}
