package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// anthropicToChatState holds the streaming conversion state
type anthropicToChatState struct {
	// Metadata
	id       string
	model    string
	role     string

	// Text accumulation
	textBuffer strings.Builder

	// Tool call tracking
	currentToolCallID   string
	currentToolCallName string
	toolCallArgs        *strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
	usage       struct {
		InputTokens  int
		OutputTokens int
	}
}

// anthropicToChatPool is the sync.Pool for converter state
var anthropicToChatPool = sync.Pool{
	New: func() interface{} {
		return &anthropicToChatState{
			toolCallArgs: &strings.Builder{},
		}
	},
}

// anthropicToChatConverter implements StreamConverter
type anthropicToChatConverter struct {
	state *anthropicToChatState
}

func newAnthropicToChatConverter() StreamConverter {
	return &anthropicToChatConverter{
		state: anthropicToChatPool.Get().(*anthropicToChatState),
	}
}

func (c *anthropicToChatConverter) Convert(line []byte) []byte {
	// Parse the Anthropic SSE line
	var event struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}

	switch event.Type {
	case "message_start":
		var msg struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Role   string `json:"role"`
			Model  string `json:"model"`
			Usage  struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal(line, &msg)
		c.state.id = msg.ID
		c.state.model = msg.Model
		c.state.role = msg.Role
		c.state.hasStarted = true
		return []byte(`{"id":"`+msg.ID+`","object":"chat.completion.chunk","created":0,"model":"`+msg.Model+`","choices":[{"index":0,"delta":{"role":"`+msg.Role+`"},"finish_reason":null}]}`+"\n\n")

	case "content_block_delta":
		var delta struct {
			Type          string `json:"type"`
			Index         int    `json:"index"`
			ContentBlock  *struct {
				Type string `json:"type"`
			} `json:"content_block,omitempty"`
			TextDelta     string `json:"text_delta,omitempty"`
			InputJSONDelta string `json:"input_json_delta,omitempty"`
		}
		json.Unmarshal(line, &delta)

		// Handle text delta
		if delta.TextDelta != "" {
			c.state.textBuffer.WriteString(delta.TextDelta)
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"content":"`+escapeJSON(delta.TextDelta)+`"},"finish_reason":null}]}`+"\n\n")
		}

		// Handle tool call (input_json_delta)
		if delta.InputJSONDelta != "" && delta.ContentBlock != nil && delta.ContentBlock.Type == "tool_use" {
			// This is tool call arguments
			c.state.toolCallArgs.WriteString(delta.InputJSONDelta)
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"tool_calls":[{"id":"`+c.state.currentToolCallID+`","type":"function","function":{"arguments":"`+escapeJSON(delta.InputJSONDelta)+`"}}]},"finish_reason":null}]}`+"\n\n")
		}

	case "content_block_start":
		var block struct {
			Type          string `json:"type"`
			Index         int    `json:"index"`
			ContentBlock  struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		json.Unmarshal(line, &block)
		if block.ContentBlock.Type == "tool_use" {
			c.state.currentToolCallID = block.ContentBlock.ID
			c.state.currentToolCallName = block.ContentBlock.Name
			c.state.toolCallArgs.Reset()
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{"tool_calls":[{"id":"`+block.ContentBlock.ID+`","type":"function","function":{"name":"`+block.ContentBlock.Name+`","arguments":""}}]},"finish_reason":null}]}`+"\n\n")
		}

	case "message_delta":
		var delta struct {
			Type        string `json:"type"`
			Usage       struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		json.Unmarshal(line, &delta)
		c.state.usage.OutputTokens = delta.Usage.OutputTokens

		finishReason := ""
		switch delta.Delta.StopReason {
		case "end_turn":
			finishReason = "stop"
		case "max_tokens":
			finishReason = "length"
		case "stop_sequence":
			finishReason = "stop"
		}

		if finishReason != "" {
			c.state.hasFinished = true
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":"`+finishReason+`"}]`+"\n\n")
		}

	case "message_stop":
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":null}]}`+"\n\n")
	}

	return nil
}

func (c *anthropicToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"`+c.state.model+`","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]`+"\n\n")
	}
	return nil
}

func (c *anthropicToChatConverter) Reset() {
	c.state.textBuffer.Reset()
	c.state.toolCallArgs.Reset()
	c.state.currentToolCallID = ""
	c.state.currentToolCallName = ""
	c.state.hasStarted = false
	c.state.hasFinished = false
	c.state.id = ""
	c.state.model = ""
	c.state.role = ""
	c.state.usage.InputTokens = 0
	c.state.usage.OutputTokens = 0

	// Return to pool
	anthropicToChatPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatAnthropic, Client: convert.FormatOpenAIChatCompletions}, newAnthropicToChatConverter)
}