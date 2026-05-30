package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// responsesToChatState holds the streaming conversion state
type responsesToChatState struct {
	// Metadata
	model   string
	id      string
	created int64

	// Text accumulation
	textOpen             bool
	textBuffer           strings.Builder
	reasoningBuffer      strings.Builder
	reasoningOpaqueStore map[string]bool

	// Tool call tracking
	toolCallIDToIndex map[string]int
	toolCallNames     map[string]string
	toolCallArgs      map[string]*strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// responsesToChatPool is the sync.Pool for converter state
var responsesToChatPool = sync.Pool{
	New: func() interface{} {
		return &responsesToChatState{
			toolCallIDToIndex:    make(map[string]int),
			toolCallNames:        make(map[string]string),
			toolCallArgs:         make(map[string]*strings.Builder),
			reasoningOpaqueStore: make(map[string]bool),
		}
	},
}

// responsesToChatConverter implements StreamConverter
type responsesToChatConverter struct {
	state *responsesToChatState
}

func newResponsesToChatConverter() StreamConverter {
	return &responsesToChatConverter{
		state: responsesToChatPool.Get().(*responsesToChatState),
	}
}

func (c *responsesToChatConverter) Convert(line []byte) []byte {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	// Parse the SSE line
	var event struct {
		Type    string          `json:"type"`
		Delta   json.RawMessage `json:"delta,omitempty"`
		Item    json.RawMessage `json:"item,omitempty"`
		ItemID  string          `json:"item_id,omitempty"`
		Index   int             `json:"index,omitempty"`
		Content json.RawMessage `json:"content,omitempty"`
	}

	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	switch event.Type {
	case "response.created":
		c.state.hasStarted = true
		var meta struct {
			ID       string `json:"id"`
			Model    string `json:"model"`
			Role     string `json:"role"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		json.Unmarshal([]byte(data), &meta)
		if meta.ID == "" {
			meta.ID = meta.Response.ID
		}
		if meta.Model == "" {
			meta.Model = meta.Response.Model
		}
		c.state.id = meta.ID
		c.state.model = meta.Model
		return chatChunk(meta.ID, meta.Model, map[string]interface{}{"role": "assistant"}, nil, nil)

	case "response.output_text.delta":
		text := responsesTextDelta(event.Delta)
		c.state.textBuffer.WriteString(text)
		return chatChunk(c.state.id, c.state.model, map[string]interface{}{"content": text}, nil, nil)

	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		text := responsesTextDelta(event.Delta)
		c.state.reasoningBuffer.WriteString(text)
		return chatChunk(c.state.id, c.state.model, reasoningTextDelta(text, 0, ""), nil, nil)

	case "response.reasoning.done", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		text := responsesTextDoneText([]byte(data), event.Delta)
		return c.emitMissingReasoning(text)

	case "response.output_text.done":
		text := responsesTextDoneText([]byte(data), event.Delta)
		return c.emitMissingText(text)

	case "response.output_item.added":
		// Could be message or function_call item
		var item struct {
			Type   string                     `json:"type"`
			ID     string                     `json:"id"`
			CallID string                     `json:"call_id,omitempty"`
			Name   string                     `json:"name,omitempty"`
			Extra  map[string]json.RawMessage `json:"-"`
		}
		json.Unmarshal(event.Item, &item)
		if item.Type == "reasoning" {
			return c.emitReasoningOpaqueFromRaw(event.Item)
		}
		if item.Type == "function_call" && item.CallID != "" {
			idx := len(c.state.toolCallIDToIndex)
			c.state.toolCallIDToIndex[item.CallID] = idx
			c.state.toolCallNames[item.CallID] = item.Name
			c.state.toolCallArgs[item.CallID] = &strings.Builder{}
			// Emit tool call start
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"tool_calls": []interface{}{
				map[string]interface{}{"index": idx, "id": item.CallID, "type": "function", "function": map[string]interface{}{"name": item.Name, "arguments": ""}},
			}}, nil, nil)
		}
		return nil

	case "response.output_item.done":
		var item struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content,omitempty"`
		}
		var envelope struct {
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal([]byte(data), &envelope) == nil && len(envelope.Item) > 0 {
			_ = json.Unmarshal(envelope.Item, &item)
			if item.Type == "reasoning" {
				return c.emitReasoningOpaque(item.EncryptedContent)
			}
		}
		return nil

	case "response.function_call_arguments.delta":
		var delta struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		json.Unmarshal(event.Delta, &delta)
		if delta.Arguments == "" {
			delta.Arguments = responsesTextDelta(event.Delta)
		}
		if delta.CallID == "" {
			delta.CallID = event.ItemID
		}
		if args, ok := c.state.toolCallArgs[delta.CallID]; ok {
			args.WriteString(delta.Arguments)
			idx := c.state.toolCallIDToIndex[delta.CallID]
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"tool_calls": []interface{}{
				map[string]interface{}{"index": idx, "id": delta.CallID, "type": "function", "function": map[string]interface{}{"arguments": delta.Arguments}},
			}}, nil, nil)
		}
		return nil

	case "response.completed":
		c.state.hasFinished = true
		var completed struct {
			Response struct {
				ID     string                 `json:"id"`
				Model  string                 `json:"model"`
				Output []responsesOutputItem  `json:"output"`
				Usage  map[string]interface{} `json:"usage"`
			} `json:"response"`
		}
		_ = json.Unmarshal([]byte(data), &completed)
		if c.state.id == "" {
			c.state.id = completed.Response.ID
		}
		if c.state.model == "" {
			c.state.model = completed.Response.Model
		}
		var out []byte
		for _, item := range completed.Response.Output {
			if item.Type == "reasoning" {
				for _, summary := range item.Summary {
					out = append(out, c.emitMissingReasoning(summary.Text)...)
				}
				out = append(out, c.emitReasoningOpaque(item.EncryptedContent)...)
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
				out = append(out, c.emitMissingText(text)...)
			}
		}
		out = append(out, chatChunk(c.state.id, c.state.model, map[string]interface{}{}, "stop", responsesUsageToChat(completed.Response.Usage))...)
		return out

	case "response.incomplete":
		return chatChunk(c.state.id, c.state.model, map[string]interface{}{}, "length", nil)
	}

	return nil
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

func responsesTextDoneText(data []byte, raw json.RawMessage) string {
	var event struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &event); err == nil && event.Text != "" {
		return event.Text
	}
	return responsesTextDelta(raw)
}

func (c *responsesToChatConverter) emitMissingText(text string) []byte {
	if text == "" {
		return nil
	}
	current := c.state.textBuffer.String()
	if strings.HasPrefix(text, current) {
		missing := strings.TrimPrefix(text, current)
		if missing == "" {
			return nil
		}
		c.state.textBuffer.WriteString(missing)
		return chatChunk(c.state.id, c.state.model, map[string]interface{}{"content": missing}, nil, nil)
	}
	if strings.Contains(current, text) {
		return nil
	}
	c.state.textBuffer.WriteString(text)
	return chatChunk(c.state.id, c.state.model, map[string]interface{}{"content": text}, nil, nil)
}

func (c *responsesToChatConverter) emitMissingReasoning(text string) []byte {
	if text == "" {
		return nil
	}
	current := c.state.reasoningBuffer.String()
	if strings.HasPrefix(text, current) {
		missing := strings.TrimPrefix(text, current)
		if missing == "" {
			return nil
		}
		c.state.reasoningBuffer.WriteString(missing)
		return chatChunk(c.state.id, c.state.model, reasoningTextDelta(missing, 0, ""), nil, nil)
	}
	if strings.Contains(current, text) {
		return nil
	}
	c.state.reasoningBuffer.WriteString(text)
	return chatChunk(c.state.id, c.state.model, reasoningTextDelta(text, 0, ""), nil, nil)
}

func (c *responsesToChatConverter) emitReasoningOpaqueFromRaw(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var item struct {
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	return c.emitReasoningOpaque(item.EncryptedContent)
}

func (c *responsesToChatConverter) emitReasoningOpaque(value string) []byte {
	if value == "" {
		return nil
	}
	if c.state.reasoningOpaqueStore[value] {
		return nil
	}
	c.state.reasoningOpaqueStore[value] = true
	return chatChunk(c.state.id, c.state.model, reasoningEncryptedDelta(0, value), nil, nil)
}

func responsesUsageToChat(usage map[string]interface{}) map[string]interface{} {
	if len(usage) == 0 {
		return nil
	}
	prompt := numericUsageValue(usage, "prompt_tokens", "input_tokens")
	completion := numericUsageValue(usage, "completion_tokens", "output_tokens")
	total := numericUsageValue(usage, "total_tokens")
	if total == 0 && (prompt != 0 || completion != 0) {
		total = prompt + completion
	}
	if prompt == 0 && completion == 0 && total == 0 {
		return nil
	}
	return map[string]interface{}{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      total,
	}
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

func (c *responsesToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return chatChunk(c.state.id, c.state.model, map[string]interface{}{}, "stop", nil)
	}
	return nil
}

func (c *responsesToChatConverter) Reset() {
	// Clear all maps and buffers
	for k := range c.state.toolCallIDToIndex {
		delete(c.state.toolCallIDToIndex, k)
	}
	for k := range c.state.toolCallNames {
		delete(c.state.toolCallNames, k)
	}
	for k := range c.state.toolCallArgs {
		if v := c.state.toolCallArgs[k]; v != nil {
			v.Reset()
		}
		delete(c.state.toolCallArgs, k)
	}
	c.state.textBuffer.Reset()
	c.state.reasoningBuffer.Reset()
	for k := range c.state.reasoningOpaqueStore {
		delete(c.state.reasoningOpaqueStore, k)
	}
	c.state.hasStarted = false
	c.state.hasFinished = false
	c.state.model = ""
	c.state.id = ""
	c.state.created = 0
	c.state.textOpen = false

	// Return to pool
	responsesToChatPool.Put(c.state)
	c.state = nil
}

// escapeJSON escapes a string for safe embedding in JSON
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIResponses, Client: convert.FormatOpenAIChatCompletions}, newResponsesToChatConverter)
}
