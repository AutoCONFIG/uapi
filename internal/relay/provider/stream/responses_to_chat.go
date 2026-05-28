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
	model    string
	id       string
	created  int64

	// Text accumulation
	textOpen   bool
	textBuffer strings.Builder

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
			toolCallIDToIndex: make(map[string]int),
			toolCallNames:    make(map[string]string),
			toolCallArgs:     make(map[string]*strings.Builder),
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
	// Parse the SSE line
	var event struct {
		Type    string          `json:"type"`
		Delta   json.RawMessage `json:"delta,omitempty"`
		Item    json.RawMessage `json:"item,omitempty"`
		ItemID  string          `json:"item_id,omitempty"`
		Index   int             `json:"index,omitempty"`
		Content json.RawMessage `json:"content,omitempty"`
	}

	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}

	switch event.Type {
	case "response.created":
		c.state.hasStarted = true
		var meta struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Role  string `json:"role"`
		}
		json.Unmarshal(line, &meta)
		c.state.id = meta.ID
		c.state.model = meta.Model
		return []byte(`{"id":"` + meta.ID + `","object":"chat.completion.chunk","created":0,"model":"` + meta.Model + `","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n")

	case "response.output_text.delta":
		var delta struct {
			Text string `json:"text"`
		}
		json.Unmarshal(event.Delta, &delta)
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"content":"`+escapeJSON(delta.Text)+`"},"finish_reason":null}]}` + "\n\n")

	case "response.output_item.added":
		// Could be message or function_call item
		var item struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			CallID string `json:"call_id,omitempty"`
			Name   string `json:"name,omitempty"`
		}
		json.Unmarshal(event.Item, &item)
		if item.Type == "function_call" && item.CallID != "" {
			idx := len(c.state.toolCallIDToIndex)
			c.state.toolCallIDToIndex[item.CallID] = idx
			c.state.toolCallNames[item.CallID] = item.Name
			c.state.toolCallArgs[item.CallID] = &strings.Builder{}
			// Emit tool call start
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"tool_calls":[{"id":"`+item.CallID+`","type":"function","function":{"name":"`+item.Name+`","arguments":""}}]},"finish_reason":null}]}` + "\n\n")
		}
		return nil

	case "response.function_call_arguments.delta":
		var delta struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		json.Unmarshal(event.Delta, &delta)
		if args, ok := c.state.toolCallArgs[delta.CallID]; ok {
			args.WriteString(delta.Arguments)
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"tool_calls":[{"id":"`+delta.CallID+`","type":"function","function":{"arguments":"`+escapeJSON(delta.Arguments)+`"}}]},"finish_reason":null}]}` + "\n\n")
		}
		return nil

	case "response.completed":
		c.state.hasFinished = true
		// Try to parse usage from the event if needed
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]` + "\n\n")

	case "response.incomplete":
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":"length"}]` + "\n\n")
	}

	return nil
}

func (c *responsesToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]` + "\n\n")
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