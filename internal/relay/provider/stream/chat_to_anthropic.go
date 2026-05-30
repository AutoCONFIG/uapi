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
	currentToolCallID     string
	currentToolCallName   string
	toolCallArgs          *strings.Builder
	currentToolBlockIndex int

	// State flags
	hasStarted          bool
	hasThinkingBlock    bool
	hasStoppedThinking  bool
	thinkingBlockIndex  int
	hasTextBlock        bool
	hasStoppedBlock     bool
	textBlockIndex      int
	hasToolBlock        bool
	hasStoppedToolBlock bool
	hasFinished         bool
	nextBlockIndex      int
}

// chatToAnthropicPool is the sync.Pool for converter state
var chatToAnthropicPool = sync.Pool{
	New: func() interface{} {
		return &chatToAnthropicState{
			toolCallArgs:          &strings.Builder{},
			currentToolBlockIndex: -1,
			thinkingBlockIndex:    -1,
			textBlockIndex:        -1,
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
		Role             string          `json:"role"`
		Content          string          `json:"content"`
		ReasoningContent string          `json:"reasoning_content"`
		Reasoning        string          `json:"reasoning"`
		ReasoningDetails json.RawMessage `json:"reasoning_details"`
		ToolCalls        []struct {
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
	case deltaData.ReasoningContent != "" || deltaData.Reasoning != "" || len(deltaData.ReasoningDetails) > 0:
		return c.reasoningEvents(deltaData.ReasoningContent, deltaData.Reasoning, deltaData.ReasoningDetails)

	// First message with role
	case c.state.id == "" && event.ID != "" && deltaData.Role != "" && deltaData.Content == "" && len(deltaData.ToolCalls) == 0:
		c.state.id = event.ID
		c.state.model = event.Model
		c.state.hasStarted = true

		if deltaData.Role != "" {
			return c.messageStartEvent()
		}
		return nil

	// Content delta
	case deltaData.Content != "":
		if c.state.id == "" {
			c.state.id = event.ID
			c.state.model = event.Model
			c.state.hasStarted = true
		}
		out := c.ensureThinkingBlockStopped()
		out = append(out, c.ensureToolBlockStopped()...)
		out = append(out, c.ensureTextBlockStarted()...)
		return append(out, sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": c.state.textBlockIndex, "delta": map[string]interface{}{"type": "text_delta", "text": deltaData.Content}})...)

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		for _, tc := range deltaData.ToolCalls {
			if tc.Function.Name != "" && tc.Function.Arguments == "" {
				// Tool call start - emit content_block_start
				c.state.currentToolCallID = tc.ID
				c.state.currentToolCallName = tc.Function.Name
				c.state.toolCallArgs.Reset()
				out := c.ensureThinkingBlockStopped()
				out = append(out, c.ensureTextBlockStopped()...)
				c.startToolBlock()
				return append(out, sseJSON(map[string]interface{}{"type": "content_block_start", "index": c.state.currentToolBlockIndex, "content_block": map[string]interface{}{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]interface{}{}}})...)
			} else if tc.Function.Arguments != "" {
				// Tool call arguments delta - emit content_block_delta with input_json_delta
				c.state.toolCallArgs.WriteString(tc.Function.Arguments)
				return sseJSON(map[string]interface{}{"type": "content_block_delta", "index": c.state.currentToolBlockIndex, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments}})
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
			out := c.ensureThinkingBlockStopped()
			out = append(out, c.ensureTextBlockStopped()...)
			out = append(out, c.ensureToolBlockStopped()...)
			return append(out, c.messageDeltaAndStopEvents(anthropicReason)...)
		}
		return nil
	}

	return nil
}

func (c *chatToAnthropicConverter) Done() []byte {
	if !c.state.hasFinished {
		out := c.ensureThinkingBlockStopped()
		out = append(out, c.ensureTextBlockStopped()...)
		out = append(out, c.ensureToolBlockStopped()...)
		return append(out, c.messageDeltaAndStopEvents("end_turn")...)
	}
	return nil
}

func (c *chatToAnthropicConverter) messageStartEvent() []byte {
	return sseEventJSON("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":    c.state.id,
			"type":  "message",
			"model": c.state.model,
			"role":  "assistant",
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (c *chatToAnthropicConverter) ensureMessageStarted() []byte {
	if c.state.hasStarted {
		return nil
	}
	if c.state.id == "" {
		c.state.id = randomID("msg_")
	}
	c.state.hasStarted = true
	return c.messageStartEvent()
}

func (c *chatToAnthropicConverter) ensureTextBlockStarted() []byte {
	out := c.ensureMessageStarted()
	if c.state.hasTextBlock {
		return out
	}
	c.state.hasTextBlock = true
	c.state.textBlockIndex = c.state.nextBlockIndex
	c.state.nextBlockIndex++
	return append(out, sseEventJSON("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         c.state.textBlockIndex,
		"content_block": map[string]interface{}{"type": "text", "text": ""},
	})...)
}

func (c *chatToAnthropicConverter) ensureThinkingBlockStarted() []byte {
	out := c.ensureMessageStarted()
	if c.state.hasThinkingBlock {
		return out
	}
	c.state.hasThinkingBlock = true
	c.state.thinkingBlockIndex = c.state.nextBlockIndex
	c.state.nextBlockIndex++
	return append(out, sseEventJSON("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         c.state.thinkingBlockIndex,
		"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
	})...)
}

func (c *chatToAnthropicConverter) ensureThinkingBlockStopped() []byte {
	if !c.state.hasThinkingBlock || c.state.hasStoppedThinking {
		return nil
	}
	c.state.hasStoppedThinking = true
	return sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": c.state.thinkingBlockIndex})
}

func (c *chatToAnthropicConverter) ensureTextBlockStopped() []byte {
	if !c.state.hasTextBlock || c.state.hasStoppedBlock {
		return nil
	}
	c.state.hasStoppedBlock = true
	return sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": c.state.textBlockIndex})
}

func (c *chatToAnthropicConverter) startToolBlock() {
	if c.state.hasToolBlock && !c.state.hasStoppedToolBlock {
		return
	}
	c.state.hasToolBlock = true
	c.state.hasStoppedToolBlock = false
	c.state.currentToolBlockIndex = c.state.nextBlockIndex
	c.state.nextBlockIndex++
}

func (c *chatToAnthropicConverter) ensureToolBlockStopped() []byte {
	if !c.state.hasToolBlock || c.state.hasStoppedToolBlock {
		return nil
	}
	c.state.hasStoppedToolBlock = true
	return sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": c.state.currentToolBlockIndex})
}

func (c *chatToAnthropicConverter) reasoningEvents(reasoningContent, reasoning string, detailsRaw json.RawMessage) []byte {
	var out []byte
	details := parseChatReasoningDetails(detailsRaw)
	if len(details) > 0 {
		for _, detail := range details {
			text := reasoningDetailText(detail)
			if text != "" {
				blockOut := c.ensureThinkingBlockStarted()
				out = append(out, blockOut...)
				out = append(out, sseEventJSON("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": c.state.thinkingBlockIndex,
					"delta": map[string]interface{}{"type": "thinking_delta", "thinking": text},
				})...)
			}
			if sig := reasoningDetailSignature(detail); sig != "" {
				blockOut := c.ensureThinkingBlockStarted()
				out = append(out, blockOut...)
				out = append(out, sseEventJSON("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": c.state.thinkingBlockIndex,
					"delta": map[string]interface{}{"type": "signature_delta", "signature": sig},
				})...)
			} else if encrypted := reasoningDetailEncrypted(detail); encrypted != "" && text == "" {
				blockOut := c.ensureMessageStarted()
				out = append(out, blockOut...)
				idx := c.state.nextBlockIndex
				c.state.nextBlockIndex++
				out = append(out, sseEventJSON("content_block_start", map[string]interface{}{
					"type":          "content_block_start",
					"index":         idx,
					"content_block": map[string]interface{}{"type": "redacted_thinking", "data": encrypted},
				})...)
				out = append(out, sseEventJSON("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": idx})...)
			}
		}
		return out
	}
	text := reasoningContent
	if text == "" {
		text = reasoning
	}
	out = append(out, c.ensureThinkingBlockStarted()...)
	return append(out, sseEventJSON("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": c.state.thinkingBlockIndex, "delta": map[string]interface{}{"type": "thinking_delta", "thinking": text}})...)
}

func (c *chatToAnthropicConverter) messageDeltaAndStopEvents(reason string) []byte {
	c.state.hasFinished = true
	return append(
		sseEventJSON("message_delta", map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": reason}, "usage": map[string]interface{}{"output_tokens": 0}}),
		sseEventJSON("message_stop", map[string]interface{}{"type": "message_stop"})...,
	)
}

func (c *chatToAnthropicConverter) Reset() {
	c.state.toolCallArgs.Reset()
	c.state.currentToolCallID = ""
	c.state.currentToolCallName = ""
	c.state.currentToolBlockIndex = -1
	c.state.hasStarted = false
	c.state.hasThinkingBlock = false
	c.state.hasStoppedThinking = false
	c.state.thinkingBlockIndex = -1
	c.state.hasTextBlock = false
	c.state.hasStoppedBlock = false
	c.state.textBlockIndex = -1
	c.state.hasToolBlock = false
	c.state.hasStoppedToolBlock = false
	c.state.hasFinished = false
	c.state.nextBlockIndex = 0
	c.state.id = ""
	c.state.model = ""

	// Return to pool
	chatToAnthropicPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: convert.FormatAnthropic}, newChatToAnthropicConverter)
}
