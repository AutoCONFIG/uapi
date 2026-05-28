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
	id       string
	model    string

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
	// Parse the Chat SSE line
	var event struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index         int             `json:"index"`
			Delta         json.RawMessage `json:"delta"`
			FinishReason  string          `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(line, &event); err != nil {
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
			ID   string `json:"id"`
			Type string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	json.Unmarshal(delta, &deltaData)

	switch {
	// First message with role
	case c.state.id == "" && event.ID != "":
		c.state.id = event.ID
		c.state.model = event.Model
		c.state.hasStarted = true

		if deltaData.Role != "" {
			return []byte(`{"type":"message_start","id":"` + event.ID + `","model":"` + event.Model + `","role":"` + deltaData.Role + `","usage":{"input_tokens":0,"output_tokens":0}}` + "\n\n")
		}
		return nil

	// Content delta
	case deltaData.Content != "":
		return []byte(`{"type":"content_block_delta","index":0,"content_block":{"type":"text"},"text_delta":"` + escapeJSON(deltaData.Content) + `"}` + "\n\n")

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		for _, tc := range deltaData.ToolCalls {
			if tc.Function.Name != "" && tc.Function.Arguments == "" {
				// Tool call start - emit content_block_start
				c.state.currentToolCallID = tc.ID
				c.state.currentToolCallName = tc.Function.Name
				c.state.toolCallArgs.Reset()
				return []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"` + tc.ID + `","name":"` + tc.Function.Name + `"}}` + "\n\n")
			} else if tc.Function.Arguments != "" {
				// Tool call arguments delta - emit content_block_delta with input_json_delta
				c.state.toolCallArgs.WriteString(tc.Function.Arguments)
				return []byte(`{"type":"content_block_delta","index":0,"content_block":{"type":"tool_use","id":"` + tc.ID + `"},"input_json_delta":"` + escapeJSON(tc.Function.Arguments) + `"}` + "\n\n")
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
			return []byte(`{"type":"message_delta","delta":{"stop_reason":"` + anthropicReason + `"},"usage":{"output_tokens":0}}` + "\n\n")
		}
		return nil
	}

	return nil
}

func (c *chatToAnthropicConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}` + "\n\n")
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