package stream

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

type anthropicIRParser struct {
	id       string
	model    string
	role     string
	finished bool
	blocks   map[int]anthropicIRBlock
}

type anthropicIRBlock struct {
	typ    string
	callID string
	name   string
}

func newAnthropicIRParser() streamIRParser {
	return &anthropicIRParser{blocks: map[int]anthropicIRBlock{}}
}

func (p *anthropicIRParser) Parse(line []byte) []relayir.StreamEvent {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	switch event.Type {
	case "message_start":
		var envelope struct {
			Message struct {
				ID    string `json:"id"`
				Role  string `json:"role"`
				Model string `json:"model"`
			} `json:"message"`
		}
		_ = json.Unmarshal([]byte(data), &envelope)
		p.id, p.model, p.role = envelope.Message.ID, envelope.Message.Model, envelope.Message.Role
		return []relayir.StreamEvent{{Type: relayir.EventResponseCreated, ResponseID: p.id, Model: p.model}, {Type: relayir.EventMessageStart, ResponseID: p.id, Model: p.model}}
	case "content_block_start":
		var block struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Data string `json:"data"`
			} `json:"content_block"`
		}
		_ = json.Unmarshal([]byte(data), &block)
		p.blocks[block.Index] = anthropicIRBlock{typ: block.ContentBlock.Type, callID: block.ContentBlock.ID, name: block.ContentBlock.Name}
		switch block.ContentBlock.Type {
		case "tool_use":
			return []relayir.StreamEvent{{Type: relayir.EventToolCallStart, ResponseID: p.id, Model: p.model, ItemIndex: block.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Meta: rawMeta("call_id", block.ContentBlock.ID, "name", block.ContentBlock.Name)}}}
		case "redacted_thinking":
			if block.ContentBlock.Data != "" {
				return []relayir.StreamEvent{{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ItemIndex: block.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: block.ContentBlock.Data}}}
			}
		}
	case "content_block_delta":
		var raw struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &raw)
		block := p.blocks[raw.Index]
		switch {
		case raw.Delta.Text != "":
			return []relayir.StreamEvent{{Type: relayir.EventContentDelta, ResponseID: p.id, Model: p.model, ItemIndex: raw.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: raw.Delta.Text}}}
		case raw.Delta.Thinking != "":
			return []relayir.StreamEvent{{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ItemIndex: raw.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: raw.Delta.Thinking}}}
		case raw.Delta.Signature != "":
			return []relayir.StreamEvent{{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: p.model, ItemIndex: raw.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Signature: raw.Delta.Signature}}}
		case raw.Delta.PartialJSON != "":
			return []relayir.StreamEvent{{Type: relayir.EventToolArgDelta, ResponseID: p.id, Model: p.model, ItemIndex: raw.Index, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: raw.Delta.PartialJSON}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Meta: rawMeta("call_id", block.callID, "name", block.name)}}}
		}
	case "message_delta":
		var delta struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &delta)
		p.finished = true
		return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: anthropicFinishToIR(delta.Delta.StopReason), NativeReason: delta.Delta.StopReason}, Usage: &relayir.Usage{InputTokens: delta.Usage.InputTokens, OutputTokens: delta.Usage.OutputTokens, TotalTokens: delta.Usage.InputTokens + delta.Usage.OutputTokens}}}
	case "message_stop":
		return nil
	}
	return nil
}

func (p *anthropicIRParser) Done() []relayir.StreamEvent {
	if p.finished || p.id == "" {
		return nil
	}
	p.finished = true
	return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: p.model, Finish: &relayir.Finish{Reason: relayir.FinishStop, NativeReason: "end_turn"}}}
}

func (p *anthropicIRParser) Reset() {
	*p = anthropicIRParser{blocks: map[int]anthropicIRBlock{}}
}

type geminiIRParser struct {
	id       string
	model    string
	started  bool
	finished bool
}

func newGeminiIRParser() streamIRParser { return &geminiIRParser{} }

func (p *geminiIRParser) Parse(line []byte) []relayir.StreamEvent {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	for _, body := range geminiBodies([]byte(data)) {
		if events := p.parseBody(body); len(events) > 0 {
			return events
		}
	}
	return nil
}

func geminiBodies(data []byte) []json.RawMessage {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	if methodRaw, ok := root["method"]; ok {
		var method string
		if json.Unmarshal(methodRaw, &method) == nil && method == "generateContentStream" {
			return []json.RawMessage{root["params"]}
		}
	}
	if responseRaw, ok := root["response"]; ok {
		var responses []json.RawMessage
		if json.Unmarshal(responseRaw, &responses) == nil {
			return responses
		}
		return []json.RawMessage{responseRaw}
	}
	return []json.RawMessage{data}
}

func (p *geminiIRParser) parseBody(body []byte) []relayir.StreamEvent {
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text             string `json:"text"`
					Thought          bool   `json:"thought,omitempty"`
					ThoughtSignature string `json:"thoughtSignature,omitempty"`
					FunctionCall     *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall,omitempty"`
					FunctionResponse *struct {
						Name     string          `json:"name"`
						Response json.RawMessage `json:"response"`
					} `json:"functionResponse,omitempty"`
					ExecutableCode *struct {
						Language string `json:"language"`
						Code     string `json:"code"`
					} `json:"executableCode,omitempty"`
					CodeExecutionResult *struct {
						Output string `json:"output"`
					} `json:"codeExecutionResult,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason,omitempty"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata,omitempty"`
		ModelVersion string `json:"modelVersion,omitempty"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	if p.model == "" {
		p.model = response.ModelVersion
	}
	if p.id == "" {
		p.id = randomID("chatcmpl-")
	}
	var out []relayir.StreamEvent
	if !p.started && len(response.Candidates) > 0 {
		p.started = true
		out = append(out, relayir.StreamEvent{Type: relayir.EventResponseCreated, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini")})
		out = append(out, relayir.StreamEvent{Type: relayir.EventMessageStart, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini")})
	}
	if len(response.Candidates) == 0 {
		if response.UsageMetadata != nil {
			out = append(out, relayir.StreamEvent{Type: relayir.EventUsage, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), Usage: geminiUsageToIR(response.UsageMetadata.PromptTokenCount, response.UsageMetadata.CandidatesTokenCount)})
		}
		return out
	}
	candidate := response.Candidates[0]
	for idx, part := range candidate.Content.Parts {
		switch {
		case part.Text != "" && part.Thought:
			out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemReasoning, Text: part.Text, Signature: part.ThoughtSignature}})
		case part.Text != "":
			out = append(out, relayir.StreamEvent{Type: relayir.EventContentDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: part.Text}})
		case part.ThoughtSignature != "":
			out = append(out, relayir.StreamEvent{Type: relayir.EventReasoningDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemEncryptedReasoning, Signature: part.ThoughtSignature}})
		case part.FunctionCall != nil:
			callID := randomID("call_")
			out = append(out, relayir.StreamEvent{Type: relayir.EventToolCallStart, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Meta: rawMeta("call_id", callID, "name", part.FunctionCall.Name)}})
			if len(part.FunctionCall.Args) > 0 {
				out = append(out, relayir.StreamEvent{Type: relayir.EventToolArgDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemToolUse, Arguments: string(part.FunctionCall.Args)}, Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Meta: rawMeta("call_id", callID, "name", part.FunctionCall.Name)}})
			}
		case part.FunctionResponse != nil:
			out = append(out, relayir.StreamEvent{Type: relayir.EventContentDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: geminiFunctionResponseText(part.FunctionResponse.Response)}})
		case part.ExecutableCode != nil:
			text := "```" + part.ExecutableCode.Language + "\n" + part.ExecutableCode.Code + "\n```"
			out = append(out, relayir.StreamEvent{Type: relayir.EventContentDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: text}})
		case part.CodeExecutionResult != nil:
			out = append(out, relayir.StreamEvent{Type: relayir.EventContentDelta, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), ItemIndex: idx, Delta: relayir.ItemDelta{Kind: relayir.ItemText, Text: part.CodeExecutionResult.Output}})
		}
	}
	if candidate.FinishReason != "" && candidate.FinishReason != "NOT_STARTED" && candidate.FinishReason != "SPECIFIED" {
		p.finished = true
		out = append(out, relayir.StreamEvent{Type: relayir.EventResponseDone, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), Finish: &relayir.Finish{Reason: geminiFinishToIR(candidate.FinishReason), NativeReason: candidate.FinishReason}, Usage: geminiUsageFromMetadata(response.UsageMetadata)})
	}
	return out
}

func (p *geminiIRParser) Done() []relayir.StreamEvent {
	if p.finished || p.id == "" {
		return nil
	}
	p.finished = true
	return []relayir.StreamEvent{{Type: relayir.EventResponseDone, ResponseID: p.id, Model: firstNonEmpty(p.model, "gemini"), Finish: &relayir.Finish{Reason: relayir.FinishStop, NativeReason: "STOP"}}}
}

func (p *geminiIRParser) Reset() { *p = geminiIRParser{} }

type anthropicIREmitter struct {
	id                     string
	model                  string
	started                bool
	finished               bool
	nextBlockIndex         int
	thinkingBlockIndex     int
	textBlockIndex         int
	thinkingStarted        bool
	thinkingStopped        bool
	textStarted            bool
	textStopped            bool
	toolBlockIndexByCall   map[string]int
	toolBlockStoppedByCall map[string]bool
}

func newAnthropicIREmitter() streamIREmitter {
	return &anthropicIREmitter{
		thinkingBlockIndex:     -1,
		textBlockIndex:         -1,
		toolBlockIndexByCall:   map[string]int{},
		toolBlockStoppedByCall: map[string]bool{},
	}
}

func (e *anthropicIREmitter) Emit(event relayir.StreamEvent) []byte {
	e.setMeta(event)
	switch event.Type {
	case relayir.EventResponseCreated, relayir.EventMessageStart:
		return e.ensureMessageStarted()
	case relayir.EventReasoningDelta, relayir.EventReasoningEnd:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			out := e.ensureMessageStarted()
			idx := e.nextBlockIndex
			e.nextBlockIndex++
			out = append(out, sseEventJSON("content_block_start", map[string]interface{}{"type": "content_block_start", "index": idx, "content_block": map[string]interface{}{"type": "redacted_thinking", "data": event.Delta.Signature}})...)
			return append(out, sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": idx})...)
		}
		out := e.ensureThinkingStarted()
		if event.Delta.Text != "" {
			out = append(out, sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": e.thinkingBlockIndex, "delta": map[string]interface{}{"type": "thinking_delta", "thinking": event.Delta.Text}})...)
		}
		if event.Delta.Signature != "" {
			out = append(out, sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": e.thinkingBlockIndex, "delta": map[string]interface{}{"type": "signature_delta", "signature": event.Delta.Signature}})...)
		}
		return out
	case relayir.EventContentDelta, relayir.EventContentPartEnd:
		if event.Delta.Text == "" {
			return nil
		}
		out := e.stopThinking()
		out = append(out, e.ensureTextStarted()...)
		return append(out, sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": e.textBlockIndex, "delta": map[string]interface{}{"type": "text_delta", "text": event.Delta.Text}})...)
	case relayir.EventToolCallStart:
		out := e.stopThinking()
		out = append(out, e.stopText()...)
		callID := rawMetaString(event.Native.Meta, "call_id")
		name := rawMetaString(event.Native.Meta, "name")
		if callID == "" {
			callID = randomID("toolu_")
		}
		idx := e.nextBlockIndex
		e.nextBlockIndex++
		e.toolBlockIndexByCall[callID] = idx
		return append(out, sseEventJSON("content_block_start", map[string]interface{}{"type": "content_block_start", "index": idx, "content_block": map[string]interface{}{"type": "tool_use", "id": callID, "name": name, "input": map[string]interface{}{}}})...)
	case relayir.EventToolArgDelta:
		callID := rawMetaString(event.Native.Meta, "call_id")
		return sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": e.toolBlockIndexByCall[callID], "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": event.Delta.Arguments}})
	case relayir.EventResponseDone:
		out := e.stopThinking()
		out = append(out, e.stopText()...)
		out = append(out, e.stopTools()...)
		return append(out, e.messageDeltaAndStop(event.Finish)...)
	}
	return nil
}

func (e *anthropicIREmitter) setMeta(event relayir.StreamEvent) {
	if e.id == "" && event.ResponseID != "" {
		e.id = event.ResponseID
	}
	if e.model == "" && event.Model != "" {
		e.model = event.Model
	}
}

func (e *anthropicIREmitter) ensureMessageStarted() []byte {
	if e.started {
		return nil
	}
	e.started = true
	if e.id == "" {
		e.id = randomID("msg_")
	}
	return sseEventJSON("message_start", map[string]interface{}{"type": "message_start", "message": map[string]interface{}{"id": e.id, "type": "message", "model": e.model, "role": "assistant", "usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0}}})
}

func (e *anthropicIREmitter) ensureThinkingStarted() []byte {
	out := e.ensureMessageStarted()
	if e.thinkingStarted {
		return out
	}
	e.thinkingStarted = true
	e.thinkingBlockIndex = e.nextBlockIndex
	e.nextBlockIndex++
	return append(out, sseEventJSON("content_block_start", map[string]interface{}{"type": "content_block_start", "index": e.thinkingBlockIndex, "content_block": map[string]interface{}{"type": "thinking", "thinking": ""}})...)
}

func (e *anthropicIREmitter) ensureTextStarted() []byte {
	out := e.ensureMessageStarted()
	if e.textStarted {
		return out
	}
	e.textStarted = true
	e.textBlockIndex = e.nextBlockIndex
	e.nextBlockIndex++
	return append(out, sseEventJSON("content_block_start", map[string]interface{}{"type": "content_block_start", "index": e.textBlockIndex, "content_block": map[string]interface{}{"type": "text", "text": ""}})...)
}

func (e *anthropicIREmitter) stopThinking() []byte {
	if !e.thinkingStarted || e.thinkingStopped {
		return nil
	}
	e.thinkingStopped = true
	return sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": e.thinkingBlockIndex})
}

func (e *anthropicIREmitter) stopText() []byte {
	if !e.textStarted || e.textStopped {
		return nil
	}
	e.textStopped = true
	return sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": e.textBlockIndex})
}

func (e *anthropicIREmitter) stopTools() []byte {
	var out []byte
	for callID, idx := range e.toolBlockIndexByCall {
		if e.toolBlockStoppedByCall[callID] {
			continue
		}
		e.toolBlockStoppedByCall[callID] = true
		out = append(out, sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": idx})...)
	}
	return out
}

func (e *anthropicIREmitter) messageDeltaAndStop(finish *relayir.Finish) []byte {
	if e.finished {
		return nil
	}
	e.finished = true
	reason := "end_turn"
	if finish != nil {
		switch finish.Reason {
		case relayir.FinishMaxTokens:
			reason = "max_tokens"
		case relayir.FinishToolCall:
			reason = "tool_use"
		}
	}
	return append(sseEventJSON("message_delta", map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": reason}, "usage": map[string]interface{}{"output_tokens": 0}}), sseEventJSON("message_stop", map[string]interface{}{"type": "message_stop"})...)
}

func (e *anthropicIREmitter) Done() []byte {
	out := e.stopThinking()
	out = append(out, e.stopText()...)
	out = append(out, e.stopTools()...)
	return append(out, e.messageDeltaAndStop(&relayir.Finish{Reason: relayir.FinishStop})...)
}

func (e *anthropicIREmitter) Reset() { *e = *newAnthropicIREmitter().(*anthropicIREmitter) }

type geminiIREmitter struct {
	finished      bool
	toolCallNames map[int]string
	toolCallArgs  map[int]*strings.Builder
}

func newGeminiIREmitter() streamIREmitter {
	return &geminiIREmitter{toolCallNames: map[int]string{}, toolCallArgs: map[int]*strings.Builder{}}
}

func (e *geminiIREmitter) Emit(event relayir.StreamEvent) []byte {
	switch event.Type {
	case relayir.EventContentDelta, relayir.EventContentPartEnd:
		if event.Delta.Text == "" {
			return nil
		}
		return geminiStreamEvent(event.Delta.Text, "NOT_STARTED")
	case relayir.EventReasoningDelta, relayir.EventReasoningEnd:
		if event.Delta.Kind == relayir.ItemEncryptedReasoning || event.Delta.Signature != "" && event.Delta.Text == "" {
			return geminiThoughtSignatureStreamEvent(event.Delta.Signature, "NOT_STARTED")
		}
		return geminiThoughtStreamEvent(event.Delta.Text, event.Delta.Signature, "NOT_STARTED")
	case relayir.EventToolCallStart:
		idx := event.ItemIndex
		e.toolCallNames[idx] = rawMetaString(event.Native.Meta, "name")
		if e.toolCallArgs[idx] == nil {
			e.toolCallArgs[idx] = &strings.Builder{}
		}
	case relayir.EventToolArgDelta:
		idx := event.ItemIndex
		if e.toolCallArgs[idx] == nil {
			e.toolCallArgs[idx] = &strings.Builder{}
		}
		e.toolCallArgs[idx].WriteString(event.Delta.Arguments)
	case relayir.EventResponseDone:
		e.finished = true
		if len(e.toolCallNames) > 0 {
			return e.functionCallEvents()
		}
		return geminiStreamFinishEvent(geminiFinishFromIR(event.Finish))
	}
	return nil
}

func (e *geminiIREmitter) functionCallEvents() []byte {
	var out []byte
	for idx := range e.toolCallNames {
		out = append(out, e.functionCallEvent(idx)...)
	}
	return out
}

func (e *geminiIREmitter) functionCallEvent(idx int) []byte {
	name := e.toolCallNames[idx]
	if name == "" {
		return nil
	}
	args := map[string]interface{}{}
	if b := e.toolCallArgs[idx]; b != nil && b.Len() > 0 {
		var parsed interface{}
		if err := json.Unmarshal([]byte(b.String()), &parsed); err == nil {
			if parsedMap, ok := parsed.(map[string]interface{}); ok {
				args = parsedMap
			} else {
				args = map[string]interface{}{"value": parsed}
			}
		} else {
			args = map[string]interface{}{"arguments": b.String()}
		}
	}
	return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"functionCall": map[string]interface{}{"name": name, "args": args}}}}, "finishReason": "STOP"}}}})
}

func (e *geminiIREmitter) Done() []byte {
	if e.finished {
		return nil
	}
	e.finished = true
	if len(e.toolCallNames) > 0 {
		return e.functionCallEvents()
	}
	return geminiStreamFinishEvent("STOP")
}

func (e *geminiIREmitter) Reset() { *e = *newGeminiIREmitter().(*geminiIREmitter) }

func anthropicFinishToIR(reason string) relayir.FinishReason {
	switch reason {
	case "max_tokens":
		return relayir.FinishMaxTokens
	case "tool_use":
		return relayir.FinishToolCall
	case "stop_sequence", "end_turn":
		return relayir.FinishStop
	default:
		return relayir.FinishUnknown
	}
}

func geminiFinishToIR(reason string) relayir.FinishReason {
	switch reason {
	case "MAX_TOKENS":
		return relayir.FinishMaxTokens
	case "SAFETY":
		return relayir.FinishSafety
	case "STOP":
		return relayir.FinishStop
	default:
		return relayir.FinishUnknown
	}
}

func geminiFinishFromIR(finish *relayir.Finish) string {
	if finish == nil {
		return "STOP"
	}
	switch finish.Reason {
	case relayir.FinishMaxTokens:
		return "MAX_TOKENS"
	case relayir.FinishSafety:
		return "SAFETY"
	default:
		return "STOP"
	}
}

func geminiUsageFromMetadata(usage *struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}) *relayir.Usage {
	if usage == nil {
		return nil
	}
	return geminiUsageToIR(usage.PromptTokenCount, usage.CandidatesTokenCount)
}

func geminiUsageToIR(prompt, completion int) *relayir.Usage {
	if prompt == 0 && completion == 0 {
		return nil
	}
	return &relayir.Usage{InputTokens: prompt, OutputTokens: completion, TotalTokens: prompt + completion}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func geminiStreamEvent(text string, finishReason string) []byte {
	return sseJSON(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"role": "model", "parts": []interface{}{map[string]interface{}{"text": text}}}, "finishReason": finishReason}}})
}

func geminiThoughtStreamEvent(text string, thoughtSignature string, finishReason string) []byte {
	part := map[string]interface{}{"text": text, "thought": true}
	if thoughtSignature != "" {
		part["thoughtSignature"] = thoughtSignature
	}
	return sseJSON(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"role": "model", "parts": []interface{}{part}}, "finishReason": finishReason}}})
}

func geminiThoughtSignatureStreamEvent(thoughtSignature string, finishReason string) []byte {
	return sseJSON(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"role": "model", "parts": []interface{}{map[string]interface{}{"thoughtSignature": thoughtSignature}}}, "finishReason": finishReason}}})
}

func geminiStreamFinishEvent(finishReason string) []byte {
	return sseJSON(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"role": "model", "parts": []interface{}{}}, "finishReason": finishReason}}})
}

func geminiFunctionResponseText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &response) == nil {
		var parts []string
		for _, part := range response.Content {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	var value interface{}
	if json.Unmarshal(raw, &value) == nil {
		if body, err := json.Marshal(value); err == nil {
			return string(body)
		}
	}
	return string(raw)
}

func init() {
	RegisterIRParser(convert.FormatAnthropic, newAnthropicIRParser)
	RegisterIREmitter(convert.FormatAnthropic, newAnthropicIREmitter)
	RegisterIRParser(convert.FormatGemini, newGeminiIRParser)
	RegisterIREmitter(convert.FormatGemini, newGeminiIREmitter)
}
