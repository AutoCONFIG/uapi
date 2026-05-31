package stream

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

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
		if errEvent := chatStreamError([]byte(data)); errEvent != nil {
			p.finished = true
			return []relayir.StreamEvent{{Type: relayir.EventError, ResponseID: p.id, Model: p.model, Error: errEvent}}
		}
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
	if delta.ReasoningContent != "" {
		out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ChoiceIndex: choiceIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: delta.ReasoningContent}})
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
	id            string
	model         string
	finished      bool
	toolArgText   map[string]string
	toolCallMeta  map[string]map[string]json.RawMessage
	toolCallStart map[string]bool
}

func newResponsesIRParser() streamIRParser {
	return &responsesIRParser{
		toolArgText:   map[string]string{},
		toolCallMeta:  map[string]map[string]json.RawMessage{},
		toolCallStart: map[string]bool{},
	}
}

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
		Arguments   string          `json:"arguments,omitempty"`
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
			delta.Arguments = event.Arguments
		}
		if delta.Arguments == "" {
			delta.Arguments = responsesTextDelta(event.Delta)
		}
		if delta.CallID == "" {
			delta.CallID = event.ItemID
		}
		key := p.toolKey(event.OutputIndex, delta.CallID)
		p.toolArgText[key] += delta.Arguments
		return []relayir.StreamEvent{{Type: relayir.EventToolArgDelta, ResponseID: p.id, Model: p.model, ItemIndex: event.OutputIndex, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: delta.Arguments}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: p.toolMeta(key, delta.CallID, "")}}}
	case "response.function_call_arguments.done":
		var done struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		_ = json.Unmarshal(event.Delta, &done)
		if done.Arguments == "" {
			done.Arguments = event.Arguments
		}
		if done.Arguments == "" {
			done.Arguments = responsesTextDelta(event.Delta)
		}
		if done.CallID == "" {
			done.CallID = event.ItemID
		}
		return p.outputFunctionCallDone(event.OutputIndex, done.CallID, "", done.Arguments)
	case "error", "response.failed":
		p.finished = true
		return []relayir.StreamEvent{{Type: relayir.EventError, ResponseID: p.id, Model: p.model, Error: responsesStreamError([]byte(data))}}
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
		key := p.toolKey(index, firstNonEmpty(item.CallID, item.ID))
		meta := rawMeta("call_id", firstNonEmpty(item.CallID, item.ID), "name", item.Name)
		p.toolCallMeta[key] = meta
		p.toolCallStart[key] = true
		return []relayir.StreamEvent{{Type: relayir.EventToolCallStart, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: meta}}}
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
		ID               string `json:"id"`
		CallID           string `json:"call_id"`
		Name             string `json:"name"`
		Arguments        string `json:"arguments"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	switch item.Type {
	case "function_call":
		return p.outputFunctionCallDone(index, firstNonEmpty(item.CallID, item.ID), item.Name, item.Arguments)
	case "reasoning":
		if item.EncryptedContent == "" {
			return nil
		}
		return []relayir.StreamEvent{{Type: relayir.EventReasoningEnd, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: item.EncryptedContent}}}
	default:
		return nil
	}
}

func (p *responsesIRParser) outputFunctionCallDone(index int, callID, name, arguments string) []relayir.StreamEvent {
	key := p.toolKey(index, callID)
	meta := p.toolMeta(key, callID, name)
	var out []relayir.StreamEvent
	if !p.toolCallStart[key] {
		p.toolCallStart[key] = true
		out = append(out, relayir.StreamEvent{Type: relayir.EventToolCallStart, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: meta}})
	}
	missing := missingSuffix(p.toolArgText[key], arguments)
	if missing != "" {
		p.toolArgText[key] += missing
		out = append(out, relayir.StreamEvent{Type: relayir.EventToolArgDelta, ResponseID: p.id, Model: p.model, ItemIndex: index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: missing}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Meta: meta}})
	}
	return out
}

func (p *responsesIRParser) toolKey(index int, callID string) string {
	if callID != "" {
		return callID
	}
	return strconv.Itoa(index)
}

func (p *responsesIRParser) toolMeta(key, callID, name string) map[string]json.RawMessage {
	if meta := p.toolCallMeta[key]; len(meta) > 0 {
		if callID != "" && rawMetaString(meta, "call_id") == "" {
			meta["call_id"] = rawStringValue(callID)
		}
		if name != "" && rawMetaString(meta, "name") == "" {
			meta["name"] = rawStringValue(name)
		}
		return meta
	}
	meta := rawMeta("call_id", callID, "name", name)
	p.toolCallMeta[key] = meta
	return meta
}

func missingSuffix(current, full string) string {
	if full == "" {
		return ""
	}
	if current == "" {
		return full
	}
	if strings.HasPrefix(full, current) {
		return strings.TrimPrefix(full, current)
	}
	if strings.Contains(current, full) {
		return ""
	}
	return full
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

func (p *responsesIRParser) Reset() { *p = *newResponsesIRParser().(*responsesIRParser) }

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
	case relayir.EventError:
		e.finished = true
		return sseJSON(map[string]interface{}{"object": "error", "error": irErrorMap(event.Error)})
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
	id                string
	model             string
	created           int64
	started           bool
	finished          bool
	outputItemID      string
	outputIndex       int
	hasOutputItem     bool
	hasContentPart    bool
	outputText        strings.Builder
	reasoningItemID   string
	reasoningIndex    int
	hasReasoningItem  bool
	hasReasoningPart  bool
	reasoningText     strings.Builder
	reasoningOpaque   string
	nextOutputIndex   int
	toolCallIDToIndex map[string]int
}

func newResponsesIREmitter() streamIREmitter {
	return &responsesIREmitter{outputIndex: -1, reasoningIndex: -1, toolCallIDToIndex: map[string]int{}}
}

func (e *responsesIREmitter) Emit(event relayir.StreamEvent) []byte {
	e.setMeta(event)
	switch event.Type {
	case relayir.EventResponseCreated, relayir.EventMessageStart:
		return e.ensureStarted()
	case relayir.EventContentDelta:
		e.outputText.WriteString(event.Delta.Text)
		out := e.ensureStarted()
		out = append(out, e.ensureOutputTextPart()...)
		return append(out, sseEventJSON("response.output_text.delta", map[string]interface{}{
			"type": "response.output_text.delta", "item_id": e.outputItemID, "output_index": e.outputIndex, "content_index": 0, "delta": event.Delta.Text,
		})...)
	case relayir.EventReasoningDelta:
		out := e.ensureStarted()
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			e.reasoningOpaque = event.Delta.Signature
			return append(out, e.ensureReasoningItem()...)
		}
		e.reasoningText.WriteString(event.Delta.Text)
		out = append(out, e.ensureReasoningPart()...)
		return append(out, sseEventJSON("response.reasoning_summary_text.delta", map[string]interface{}{
			"type": "response.reasoning_summary_text.delta", "item_id": e.reasoningItemID, "output_index": e.reasoningIndex, "summary_index": 0, "delta": event.Delta.Text,
		})...)
	case relayir.EventToolCallStart:
		out := e.ensureStarted()
		callID := rawMetaString(event.Native.Meta, "call_id")
		name := rawMetaString(event.Native.Meta, "name")
		if callID == "" {
			callID = randomID("call_")
		}
		idx := len(e.toolCallIDToIndex)
		e.toolCallIDToIndex[callID] = idx
		return append(out, sseEventJSON("response.output_item.added", map[string]interface{}{
			"type": "response.output_item.added", "output_index": idx, "item": map[string]interface{}{"type": "function_call", "id": callID, "name": name, "call_id": callID},
		})...)
	case relayir.EventToolArgDelta:
		callID := rawMetaString(event.Native.Meta, "call_id")
		return sseEventJSON("response.function_call_arguments.delta", map[string]interface{}{
			"type": "response.function_call_arguments.delta", "delta": map[string]interface{}{"call_id": callID, "arguments": event.Delta.Arguments},
		})
	case relayir.EventError:
		e.finished = true
		return sseEventJSON("response.failed", map[string]interface{}{"type": "response.failed", "response": map[string]interface{}{"id": e.id, "status": "failed", "model": e.model, "error": irErrorMap(event.Error)}})
	case relayir.EventResponseDone:
		return e.completedEvent()
	}
	return nil
}

func (e *responsesIREmitter) setMeta(event relayir.StreamEvent) {
	if e.id == "" && event.ResponseID != "" {
		e.id = event.ResponseID
		e.outputItemID = e.id + "_msg"
		e.reasoningItemID = e.id + "_reasoning"
	}
	if e.model == "" && event.Model != "" {
		e.model = event.Model
	}
	if e.created == 0 {
		e.created = time.Now().Unix()
	}
}

func (e *responsesIREmitter) ensureStarted() []byte {
	if e.started {
		return nil
	}
	e.started = true
	if e.id == "" {
		e.id = randomID("resp_")
	}
	if e.outputItemID == "" {
		e.outputItemID = e.id + "_msg"
	}
	if e.reasoningItemID == "" {
		e.reasoningItemID = e.id + "_reasoning"
	}
	return sseEventJSON("response.created", map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": e.id, "object": "response", "created_at": e.created, "status": "in_progress", "model": e.model, "output": []interface{}{}}})
}

func (e *responsesIREmitter) ensureReasoningPart() []byte {
	out := e.ensureReasoningItem()
	if e.hasReasoningPart {
		return out
	}
	e.hasReasoningPart = true
	return append(out, sseEventJSON("response.reasoning_summary_part.added", map[string]interface{}{"type": "response.reasoning_summary_part.added", "item_id": e.reasoningItemID, "output_index": e.reasoningIndex, "summary_index": 0, "part": map[string]interface{}{"type": "summary_text", "text": ""}})...)
}

func (e *responsesIREmitter) ensureReasoningItem() []byte {
	if e.reasoningIndex < 0 {
		e.reasoningIndex = e.nextOutputIndex
		e.nextOutputIndex++
	}
	if e.hasReasoningItem {
		return nil
	}
	e.hasReasoningItem = true
	item := map[string]interface{}{"id": e.reasoningItemID, "type": "reasoning", "status": "in_progress", "summary": []interface{}{}}
	if e.reasoningOpaque != "" {
		item["encrypted_content"] = e.reasoningOpaque
	}
	return sseEventJSON("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": e.reasoningIndex, "item": item})
}

func (e *responsesIREmitter) ensureOutputTextPart() []byte {
	var out []byte
	if e.outputIndex < 0 {
		e.outputIndex = e.nextOutputIndex
		e.nextOutputIndex++
	}
	if !e.hasOutputItem {
		e.hasOutputItem = true
		out = append(out, sseEventJSON("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": e.outputIndex, "item": map[string]interface{}{"id": e.outputItemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []interface{}{}}})...)
	}
	if !e.hasContentPart {
		e.hasContentPart = true
		out = append(out, sseEventJSON("response.content_part.added", map[string]interface{}{"type": "response.content_part.added", "item_id": e.outputItemID, "output_index": e.outputIndex, "content_index": 0, "part": map[string]interface{}{"type": "output_text", "text": "", "annotations": []interface{}{}}})...)
	}
	return out
}

func (e *responsesIREmitter) completedEvent() []byte {
	if e.finished {
		return nil
	}
	e.finished = true
	var out []byte
	var output []interface{}
	reasoning := e.reasoningText.String()
	if e.hasReasoningItem {
		if e.hasReasoningPart {
			out = append(out, sseEventJSON("response.reasoning_summary_text.done", map[string]interface{}{"type": "response.reasoning_summary_text.done", "item_id": e.reasoningItemID, "output_index": e.reasoningIndex, "summary_index": 0, "text": reasoning})...)
			out = append(out, sseEventJSON("response.reasoning_summary_part.done", map[string]interface{}{"type": "response.reasoning_summary_part.done", "item_id": e.reasoningItemID, "output_index": e.reasoningIndex, "summary_index": 0, "part": map[string]interface{}{"type": "summary_text", "text": reasoning}})...)
		}
		summary := []interface{}{}
		if reasoning != "" {
			summary = append(summary, map[string]interface{}{"type": "summary_text", "text": reasoning})
		}
		item := map[string]interface{}{"id": e.reasoningItemID, "type": "reasoning", "status": "completed", "summary": summary}
		if e.reasoningOpaque != "" {
			item["encrypted_content"] = e.reasoningOpaque
		}
		out = append(out, sseEventJSON("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "output_index": e.reasoningIndex, "item": item})...)
		output = append(output, item)
	}
	text := e.outputText.String()
	if e.hasOutputItem {
		out = append(out, sseEventJSON("response.output_text.done", map[string]interface{}{"type": "response.output_text.done", "item_id": e.outputItemID, "output_index": e.outputIndex, "content_index": 0, "text": text})...)
		out = append(out, sseEventJSON("response.content_part.done", map[string]interface{}{"type": "response.content_part.done", "item_id": e.outputItemID, "output_index": e.outputIndex, "content_index": 0, "part": map[string]interface{}{"type": "output_text", "text": text, "annotations": []interface{}{}}})...)
		item := map[string]interface{}{"id": e.outputItemID, "type": "message", "status": "completed", "role": "assistant", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": text, "annotations": []interface{}{}}}}
		out = append(out, sseEventJSON("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "output_index": e.outputIndex, "item": item})...)
		output = append(output, item)
	}
	out = append(out, sseEventJSON("response.completed", map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": e.id, "object": "response", "created_at": e.created, "status": "completed", "model": e.model, "output": output}})...)
	return out
}

func (e *responsesIREmitter) Done() []byte { return e.completedEvent() }

func (e *responsesIREmitter) Reset() {
	*e = responsesIREmitter{outputIndex: -1, reasoningIndex: -1, toolCallIDToIndex: map[string]int{}}
}

func chatChunkFromIR(event relayir.StreamEvent) []byte {
	id := event.ResponseID
	model := event.Model
	switch event.Type {
	case relayir.EventResponseCreated, relayir.EventMessageStart:
		return chatChunk(id, model, map[string]interface{}{"role": "assistant"}, nil, nil)
	case relayir.EventContentDelta:
		return chatChunk(id, model, map[string]interface{}{"content": event.Delta.Text}, nil, nil)
	case relayir.EventContentPartEnd:
		if event.Delta.Text == "" {
			return nil
		}
		return chatChunk(id, model, map[string]interface{}{"content": event.Delta.Text}, nil, nil)
	case relayir.EventReasoningDelta:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return chatChunk(id, model, reasoningEncryptedDelta(event.ItemIndex, event.Delta.Signature), nil, nil)
		}
		return chatChunk(id, model, reasoningTextDelta(event.Delta.Text, event.ItemIndex, event.Delta.Signature), nil, nil)
	case relayir.EventReasoningEnd:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return chatChunk(id, model, reasoningEncryptedDelta(event.ItemIndex, event.Delta.Signature), nil, nil)
		}
		if event.Delta.Text == "" {
			return nil
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
		InputTokens:         numericUsageValue(usage, "prompt_tokens"),
		OutputTokens:        numericUsageValue(usage, "completion_tokens"),
		TotalTokens:         numericUsageValue(usage, "total_tokens"),
		CacheReadTokens:     streamUsageCacheReadTokens(usage),
		CacheCreationTokens: streamUsageCacheCreationTokens(usage),
		CacheWriteTokens:    streamUsageCacheCreationTokens(usage),
		InputTokenDetails:   rawUsageDetails(usage, "prompt_tokens_details"),
		OutputTokenDetails:  rawUsageDetails(usage, "completion_tokens_details"),
		PromptTokens:        numericUsageValue(usage, "prompt_tokens"),
		CompletionTokens:    numericUsageValue(usage, "completion_tokens"),
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
	return &relayir.Usage{
		InputTokens:         prompt,
		OutputTokens:        completion,
		TotalTokens:         total,
		PromptTokens:        prompt,
		CompletionTokens:    completion,
		CacheReadTokens:     streamUsageCacheReadTokens(usage),
		CacheCreationTokens: streamUsageCacheCreationTokens(usage),
		CacheWriteTokens:    streamUsageCacheCreationTokens(usage),
		InputTokenDetails:   rawUsageDetails(usage, "input_tokens_details", "prompt_tokens_details"),
		OutputTokenDetails:  rawUsageDetails(usage, "output_tokens_details", "completion_tokens_details"),
	}
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
	out := map[string]interface{}{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": total}
	if usage.CacheReadTokens > 0 || usage.CacheCreationTokens > 0 || usage.CacheWriteTokens > 0 {
		details := map[string]interface{}{}
		if usage.CacheReadTokens > 0 {
			details["cached_tokens"] = usage.CacheReadTokens
			details["cached_read_tokens"] = usage.CacheReadTokens
		}
		if usage.CacheCreationTokens > 0 {
			details["cache_creation_input_tokens"] = usage.CacheCreationTokens
			details["cached_write_tokens"] = usage.CacheCreationTokens
		} else if usage.CacheWriteTokens > 0 {
			details["cached_write_tokens"] = usage.CacheWriteTokens
		}
		out["prompt_tokens_details"] = details
	}
	return out
}

func streamUsageCacheReadTokens(usage map[string]interface{}) int {
	read := numericUsageValue(usage, "cache_read_input_tokens", "prompt_cache_hit_tokens", "cached_tokens")
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, ok := usage[key].(map[string]interface{}); ok && read == 0 {
			read = numericUsageValue(details, "cached_read_tokens", "cached_tokens", "cache_read_input_tokens")
		}
	}
	if read == 0 {
		read = numericUsageValue(usage, "cachedContentTokenCount")
	}
	return read
}

func streamUsageCacheCreationTokens(usage map[string]interface{}) int {
	creation := numericUsageValue(usage, "cache_creation_input_tokens", "cache_write_input_tokens")
	if creation == 0 {
		if nested, ok := usage["cache_creation"].(map[string]interface{}); ok {
			creation = numericUsageValue(nested, "ephemeral_5m_input_tokens") + numericUsageValue(nested, "ephemeral_1h_input_tokens")
		}
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, ok := usage[key].(map[string]interface{}); ok && creation == 0 {
			creation = numericUsageValue(details, "cached_write_tokens", "cache_creation_input_tokens")
		}
	}
	return creation
}

func rawUsageDetails(usage map[string]interface{}, keys ...string) map[string]json.RawMessage {
	for _, key := range keys {
		details, ok := usage[key].(map[string]interface{})
		if !ok || len(details) == 0 {
			continue
		}
		out := make(map[string]json.RawMessage, len(details))
		for k, v := range details {
			raw, err := json.Marshal(v)
			if err == nil {
				out[k] = raw
			}
		}
		return out
	}
	return nil
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

func responsesTextDelta(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var delta struct {
		Text      string `json:"text"`
		Content   string `json:"content"`
		Delta     string `json:"delta"`
		Arguments string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		return ""
	}
	switch {
	case delta.Text != "":
		return delta.Text
	case delta.Content != "":
		return delta.Content
	case delta.Delta != "":
		return delta.Delta
	default:
		return delta.Arguments
	}
}

type responsesOutputItem struct {
	Type             string `json:"type"`
	EncryptedContent string `json:"encrypted_content"`
	Content          []struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		OutputText string `json:"output_text"`
	} `json:"content"`
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
}

func chatStreamError(data []byte) *relayir.Error {
	var event struct {
		Object string `json:"object"`
		Error  *struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &event)
	if event.Error == nil {
		return nil
	}
	return &relayir.Error{Code: event.Error.Code, Type: event.Error.Type, Message: event.Error.Message}
}

func responsesStreamError(data []byte) *relayir.Error {
	var event struct {
		Error *struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Response struct {
			Error *struct {
				Code    string `json:"code"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	_ = json.Unmarshal(data, &event)
	src := event.Error
	if src == nil {
		src = event.Response.Error
	}
	if src == nil {
		return &relayir.Error{Type: "upstream_error", Message: "upstream stream error"}
	}
	return &relayir.Error{Code: src.Code, Type: src.Type, Message: src.Message}
}

func irErrorMap(err *relayir.Error) map[string]interface{} {
	out := map[string]interface{}{}
	if err == nil {
		out["type"] = "upstream_error"
		out["message"] = "upstream stream error"
		return out
	}
	if err.Code != "" {
		out["code"] = err.Code
	}
	if err.Type != "" {
		out["type"] = err.Type
	}
	if err.Message != "" {
		out["message"] = err.Message
	}
	return out
}

func numericUsageValue(usage map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		switch v := usage[key].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case json.Number:
			n, _ := v.Int64()
			return int(n)
		}
	}
	return 0
}

func init() {
	RegisterIRParser(convert.FormatOpenAIChatCompletions, newChatIRParser)
	RegisterIREmitter(convert.FormatOpenAIChatCompletions, newChatIREmitter)
	RegisterIRParser(convert.FormatOpenAIResponses, newResponsesIRParser)
	RegisterIREmitter(convert.FormatOpenAIResponses, newResponsesIREmitter)
	RegisterIRParser(convert.FormatCodexResponses, newResponsesIRParser)
	RegisterIREmitter(convert.FormatCodexResponses, newResponsesIREmitter)
}
