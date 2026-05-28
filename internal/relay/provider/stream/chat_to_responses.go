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
	model    string
	id       string
	created  int64

	// Tool call tracking
	toolCallIDToIndex map[string]int
	toolCallNames     map[string]string
	toolCallArgs      map[string]*strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// chatToResponsesPool is the sync.Pool for converter state
var chatToResponsesPool = sync.Pool{
	New: func() interface{} {
		return &chatToResponsesState{
			toolCallIDToIndex: make(map[string]int),
			toolCallNames:    make(map[string]string),
			toolCallArgs:     make(map[string]*strings.Builder),
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
	// Parse the Chat SSE line
	var event struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index      int             `json:"index"`
			Delta      json.RawMessage `json:"delta"`
			FinishReason string        `json:"finish_reason"`
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
		c.state.created = event.Created
		c.state.hasStarted = true

		if deltaData.Role != "" {
			return []byte(`{"type":"response.created","id":"` + event.ID + `","model":"` + event.Model + `","role":"` + deltaData.Role + `"}` + "\n\n")
		}
		return nil

	// Content delta
	case deltaData.Content != "":
		return []byte(`{"type":"response.output_text.delta","delta":{"text":"` + escapeJSON(deltaData.Content) + `"}}` + "\n\n")

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
				return []byte(`{"type":"response.output_item.added","item":{"type":"function_call","id":"` + tc.ID + `","name":"` + tc.Function.Name + `","call_id":"` + tc.ID + `"}}` + "\n\n")
			} else if tc.Function.Arguments != "" {
				// Tool call arguments delta
				if args, ok := c.state.toolCallArgs[tc.ID]; ok {
					args.WriteString(tc.Function.Arguments)
					return []byte(`{"type":"response.function_call_arguments.delta","delta":{"call_id":"` + tc.ID + `","arguments":"` + escapeJSON(tc.Function.Arguments) + `"}}` + "\n\n")
				}
			}
		}
		return nil

	// Finish reason
	case event.Choices[0].FinishReason != "":
		c.state.hasFinished = true
		finishReason := event.Choices[0].FinishReason
		if finishReason == "stop" || finishReason == "length" {
			return []byte(`{"type":"response.` + finishReason + `"}` + "\n\n")
		}
		return []byte(`{"type":"response.completed"}` + "\n\n")
	}

	return nil
}

func (c *chatToResponsesConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"type":"response.completed"}` + "\n\n")
	}
	return nil
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
	c.state.hasFinished = false
	c.state.model = ""
	c.state.id = ""
	c.state.created = 0

	// Return to pool
	chatToResponsesPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: convert.FormatOpenAIResponses}, newChatToResponsesConverter)
}