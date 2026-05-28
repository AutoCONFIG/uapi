package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// chatToAnthropicState holds the streaming conversion state
type chatToAnthropicState struct {
	// Metadata
	id    string
	model string

	// Tool call tracking
	currentToolCallID   string
	currentToolCallName string
	toolCallArgs        *strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// chatToAnthropicPool is the sync.Pool for converter state
var chatToAnthropicPool = sync.Pool{
	New: func() interface{} {
		return &chatToAnthropicState{
			toolCallArgs: &strings.Builder{},
		}
	},
}

// chatToAnthropicConverter implements StreamConverter
type chatToAnthropicConverter struct {
	state *chatToAnthropicState
}

func newChatToAnthropicConverter() StreamConverter {
	return &chatToAnthropicConverter{
		state: chatToAnthropicPool.Get().(*chatToAnthropicState),
	}
}

func (c *chatToAnthropicConverter) Convert(line []byte) []byte {
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	// Parse the Chat SSE line
	var event struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int             `json:"index"`
			Delta        json.RawMessage `json:"delta"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	if len(event.Choices) == 0 {
		return nil
	}

	delta := event.Choices[0].Delta

	// Parse delta
	var deltaData struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	json.Unmarshal(delta, &deltaData)

	switch {
	// First message with role
	case c.state.id == "" && event.ID != "" && deltaData.Role != "" && deltaData.Content == "" && len(deltaData.ToolCalls) == 0:
		c.state.id = event.ID
		c.state.model = event.Model
		c.state.hasStarted = true

		if deltaData.Role != "" {
			return sseJSON(map[string]interface{}{"type": "message_start", "message": map[string]interface{}{"id": event.ID, "type": "message", "model": event.Model, "role": deltaData.Role, "usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0}}})
		}
		return nil

	// Content delta
	case deltaData.Content != "":
		if c.state.id == "" {
			c.state.id = event.ID
			c.state.model = event.Model
			c.state.hasStarted = true
		}
		return sseJSON(map[string]interface{}{"type": "content_block_delta", "index": 0, "delta": map[string]interface{}{"type": "text_delta", "text": deltaData.Content}})

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		for _, tc := range deltaData.ToolCalls {
			if tc.Function.Name != "" && tc.Function.Arguments == "" {
				// Tool call start - emit content_block_start
				c.state.currentToolCallID = tc.ID
				c.state.currentToolCallName = tc.Function.Name
				c.state.toolCallArgs.Reset()
				return sseJSON(map[string]interface{}{"type": "content_block_start", "index": 0, "content_block": map[string]interface{}{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name}})
			} else if tc.Function.Arguments != "" {
				// Tool call arguments delta - emit content_block_delta with input_json_delta
				c.state.toolCallArgs.WriteString(tc.Function.Arguments)
				return sseJSON(map[string]interface{}{"type": "content_block_delta", "index": 0, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments}})
			}
		}
		return nil

	// Finish reason
	case event.Choices[0].FinishReason != "":
		c.state.hasFinished = true
		finishReason := event.Choices[0].FinishReason

		var anthropicReason string
		switch finishReason {
		case "stop":
			anthropicReason = "end_turn"
		case "length":
			anthropicReason = "max_tokens"
		}

		if anthropicReason != "" {
			return sseJSON(map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": anthropicReason}, "usage": map[string]interface{}{"output_tokens": 0}})
		}
		return nil
	}

	return nil
}

func (c *chatToAnthropicConverter) Done() []byte {
	if !c.state.hasFinished {
		return sseJSON(map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": "end_turn"}, "usage": map[string]interface{}{"output_tokens": 0}})
	}
	return nil
}

func (c *chatToAnthropicConverter) Reset() {
	c.state.toolCallArgs.Reset()
	c.state.currentToolCallID = ""
	c.state.currentToolCallName = ""
	c.state.hasStarted = false
	c.state.hasFinished = false
	c.state.id = ""
	c.state.model = ""

	// Return to pool
	chatToAnthropicPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: convert.FormatAnthropic}, newChatToAnthropicConverter)
}
