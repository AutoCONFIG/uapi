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
	id    string
	model string
	role  string

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
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	// Parse the Anthropic SSE line
	var event struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	switch event.Type {
	case "message_start":
		var msg struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Role  string `json:"role"`
			Model string `json:"model"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		var envelope struct {
			Message struct {
				ID    string `json:"id"`
				Role  string `json:"role"`
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		json.Unmarshal([]byte(data), &envelope)
		if envelope.Message.ID != "" {
			msg.ID = envelope.Message.ID
			msg.Role = envelope.Message.Role
			msg.Model = envelope.Message.Model
			msg.Usage = envelope.Message.Usage
		} else {
			json.Unmarshal([]byte(data), &msg)
		}
		c.state.id = msg.ID
		c.state.model = msg.Model
		c.state.role = msg.Role
		c.state.hasStarted = true
		return chatChunk(msg.ID, msg.Model, map[string]interface{}{"role": msg.Role}, nil, nil)

	case "content_block_delta":
		var delta struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock *struct {
				Type string `json:"type"`
			} `json:"content_block,omitempty"`
			TextDelta      string `json:"text_delta,omitempty"`
			ThinkingDelta  string `json:"thinking_delta,omitempty"`
			InputJSONDelta string `json:"input_json_delta,omitempty"`
		}
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
		json.Unmarshal([]byte(data), &raw)
		json.Unmarshal([]byte(data), &delta)
		if raw.Delta.Text != "" {
			delta.TextDelta = raw.Delta.Text
		}
		if raw.Delta.Thinking != "" {
			delta.ThinkingDelta = raw.Delta.Thinking
		}
		if raw.Delta.PartialJSON != "" {
			delta.InputJSONDelta = raw.Delta.PartialJSON
		}

		if delta.ThinkingDelta != "" {
			return chatChunk(c.state.id, c.state.model, reasoningTextDelta(delta.ThinkingDelta, raw.Index, ""), nil, nil)
		}
		if raw.Delta.Signature != "" {
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"reasoning_details": []interface{}{
				map[string]interface{}{
					"index":     raw.Index,
					"type":      streamReasoningTypeText,
					"signature": raw.Delta.Signature,
				},
			}}, nil, nil)
		}

		// Handle text delta
		if delta.TextDelta != "" {
			c.state.textBuffer.WriteString(delta.TextDelta)
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"content": delta.TextDelta}, nil, nil)
		}

		// Handle tool call (input_json_delta)
		if delta.InputJSONDelta != "" && delta.ContentBlock != nil && delta.ContentBlock.Type == "tool_use" {
			// This is tool call arguments
			c.state.toolCallArgs.WriteString(delta.InputJSONDelta)
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"tool_calls": []interface{}{
				map[string]interface{}{"index": raw.Index, "id": c.state.currentToolCallID, "type": "function", "function": map[string]interface{}{"arguments": delta.InputJSONDelta}},
			}}, nil, nil)
		}

	case "content_block_start":
		var block struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		json.Unmarshal([]byte(data), &block)
		if block.ContentBlock.Type == "tool_use" {
			c.state.currentToolCallID = block.ContentBlock.ID
			c.state.currentToolCallName = block.ContentBlock.Name
			c.state.toolCallArgs.Reset()
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{"tool_calls": []interface{}{
				map[string]interface{}{"index": block.Index, "id": block.ContentBlock.ID, "type": "function", "function": map[string]interface{}{"name": block.ContentBlock.Name, "arguments": ""}},
			}}, nil, nil)
		}

	case "message_delta":
		var delta struct {
			Type  string `json:"type"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		json.Unmarshal([]byte(data), &delta)
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
			return chatChunk(c.state.id, c.state.model, map[string]interface{}{}, finishReason, nil)
		}

	case "message_stop":
		return nil
	}

	return nil
}

func (c *anthropicToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return chatChunk(c.state.id, c.state.model, map[string]interface{}{}, "stop", nil)
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
