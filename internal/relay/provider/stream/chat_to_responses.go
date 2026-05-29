package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// chatToResponsesState holds the streaming conversion state
type chatToResponsesState struct {
	// Metadata
	model   string
	id      string
	created int64

	// Tool call tracking
	toolCallIDToIndex map[string]int
	toolCallNames     map[string]string
	toolCallArgs      map[string]*strings.Builder

	// State flags
	hasStarted     bool
	hasOutputItem  bool
	hasContentPart bool
	hasFinished    bool
	outputItemID   string
	outputText     strings.Builder
}

// chatToResponsesPool is the sync.Pool for converter state
var chatToResponsesPool = sync.Pool{
	New: func() interface{} {
		return &chatToResponsesState{
			toolCallIDToIndex: make(map[string]int),
			toolCallNames:     make(map[string]string),
			toolCallArgs:      make(map[string]*strings.Builder),
		}
	},
}

// chatToResponsesConverter implements StreamConverter
type chatToResponsesConverter struct {
	state *chatToResponsesState
}

func newChatToResponsesConverter() StreamConverter {
	return &chatToResponsesConverter{
		state: chatToResponsesPool.Get().(*chatToResponsesState),
	}
}

func (c *chatToResponsesConverter) Convert(line []byte) []byte {
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

	var prefix []byte
	if c.state.id == "" && event.ID != "" {
		c.state.id = event.ID
		c.state.model = event.Model
		c.state.created = event.Created
		c.state.outputItemID = event.ID + "_msg"
	}
	if !c.state.hasStarted && c.state.id != "" {
		c.state.hasStarted = true
		prefix = sseEventJSON("response.created", map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":         c.state.id,
				"object":     "response",
				"created_at": c.state.created,
				"status":     "in_progress",
				"model":      c.state.model,
				"output":     []interface{}{},
			},
		})
	}

	switch {
	// Content delta
	case deltaData.Content != "":
		if c.state.outputItemID == "" {
			c.state.outputItemID = event.ID + "_msg"
		}
		c.state.outputText.WriteString(deltaData.Content)
		out := append(prefix, c.ensureOutputTextPart()...)
		return append(out, sseEventJSON("response.output_text.delta", map[string]interface{}{
			"type":          "response.output_text.delta",
			"item_id":       c.state.outputItemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         deltaData.Content,
		})...)

	case deltaData.Role != "":
		return prefix

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		for _, tc := range deltaData.ToolCalls {
			if tc.Function.Name != "" && tc.Function.Arguments == "" {
				// Tool call start
				if _, exists := c.state.toolCallIDToIndex[tc.ID]; !exists {
					idx := len(c.state.toolCallIDToIndex)
					c.state.toolCallIDToIndex[tc.ID] = idx
					c.state.toolCallNames[tc.ID] = tc.Function.Name
					c.state.toolCallArgs[tc.ID] = &strings.Builder{}
				}
				return append(prefix, sseJSON(map[string]interface{}{"type": "response.output_item.added", "item": map[string]interface{}{"type": "function_call", "id": tc.ID, "name": tc.Function.Name, "call_id": tc.ID}})...)
			} else if tc.Function.Arguments != "" {
				// Tool call arguments delta
				if args, ok := c.state.toolCallArgs[tc.ID]; ok {
					args.WriteString(tc.Function.Arguments)
					return append(prefix, sseJSON(map[string]interface{}{"type": "response.function_call_arguments.delta", "delta": map[string]interface{}{"call_id": tc.ID, "arguments": tc.Function.Arguments}})...)
				}
			}
		}
		return prefix

	// Finish reason
	case event.Choices[0].FinishReason != "":
		c.state.hasFinished = true
		finishReason := event.Choices[0].FinishReason
		if finishReason == "stop" || finishReason == "length" {
			if finishReason == "length" {
				return append(prefix, sseEventJSON("response.incomplete", map[string]interface{}{"type": "response.incomplete"})...)
			}
			return append(prefix, c.completedEvent()...)
		}
		return append(prefix, c.completedEvent()...)
	}

	return prefix
}

func (c *chatToResponsesConverter) Done() []byte {
	if !c.state.hasFinished {
		return c.completedEvent()
	}
	return nil
}

func (c *chatToResponsesConverter) ensureOutputTextPart() []byte {
	var out []byte
	if c.state.outputItemID == "" {
		c.state.outputItemID = c.state.id + "_msg"
	}
	if !c.state.hasOutputItem {
		c.state.hasOutputItem = true
		out = append(out, sseEventJSON("response.output_item.added", map[string]interface{}{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item": map[string]interface{}{
				"id":      c.state.outputItemID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []interface{}{},
			},
		})...)
	}
	if !c.state.hasContentPart {
		c.state.hasContentPart = true
		out = append(out, sseEventJSON("response.content_part.added", map[string]interface{}{
			"type":          "response.content_part.added",
			"item_id":       c.state.outputItemID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        "",
				"annotations": []interface{}{},
			},
		})...)
	}
	return out
}

func (c *chatToResponsesConverter) completedEvent() []byte {
	c.state.hasFinished = true
	output := []interface{}{}
	var out []byte
	text := c.state.outputText.String()
	if c.state.hasOutputItem {
		out = append(out, sseEventJSON("response.output_text.done", map[string]interface{}{
			"type":          "response.output_text.done",
			"item_id":       c.state.outputItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          text,
		})...)
		out = append(out, sseEventJSON("response.content_part.done", map[string]interface{}{
			"type":          "response.content_part.done",
			"item_id":       c.state.outputItemID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        text,
				"annotations": []interface{}{},
			},
		})...)
		out = append(out, sseEventJSON("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item": map[string]interface{}{
				"id":     c.state.outputItemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type":        "output_text",
						"text":        text,
						"annotations": []interface{}{},
					},
				},
			},
		})...)
		output = append(output, map[string]interface{}{
			"id":     c.state.outputItemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "output_text",
					"text":        text,
					"annotations": []interface{}{},
				},
			},
		})
	}
	out = append(out, sseEventJSON("response.completed", map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":         c.state.id,
			"object":     "response",
			"created_at": c.state.created,
			"status":     "completed",
			"model":      c.state.model,
			"output":     output,
		},
	})...)
	return out
}

func (c *chatToResponsesConverter) Reset() {
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
	c.state.hasStarted = false
	c.state.hasOutputItem = false
	c.state.hasContentPart = false
	c.state.hasFinished = false
	c.state.model = ""
	c.state.id = ""
	c.state.created = 0
	c.state.outputItemID = ""
	c.state.outputText.Reset()

	// Return to pool
	chatToResponsesPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: convert.FormatOpenAIResponses}, newChatToResponsesConverter)
}
