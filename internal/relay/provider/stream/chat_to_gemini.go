package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// chatToGeminiState holds the streaming conversion state
type chatToGeminiState struct {
	// Metadata
	model string

	// Tool call tracking
	currentToolCallID   string
	currentToolCallName string
	toolCallArgs        *strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// chatToGeminiPool is the sync.Pool for converter state
var chatToGeminiPool = sync.Pool{
	New: func() interface{} {
		return &chatToGeminiState{
			toolCallArgs: &strings.Builder{},
		}
	},
}

// chatToGeminiConverter implements StreamConverter
type chatToGeminiConverter struct {
	state *chatToGeminiState
}

func newChatToGeminiConverter() StreamConverter {
	return &chatToGeminiConverter{
		state: chatToGeminiPool.Get().(*chatToGeminiState),
	}
}

func (c *chatToGeminiConverter) Convert(line []byte) []byte {
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

	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}

	if len(event.Choices) == 0 {
		return nil
	}

	c.state.model = event.Model

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
	case c.state.model == "" && event.Model != "":
		c.state.model = event.Model
		c.state.hasStarted = true

		if deltaData.Role != "" {
			// Gemini streaming doesn't have explicit role message, so we just start with content
			return nil
		}
		return nil

	// Content delta
	case deltaData.Content != "":
		// Gemini wrapped format: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"..."}]}}]}}
		return []byte(`{"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"` + escapeJSON(deltaData.Content) + `"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n")

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		for _, tc := range deltaData.ToolCalls {
			if tc.Function.Name != "" && tc.Function.Arguments == "" {
				// Tool call start
				c.state.currentToolCallID = tc.ID
				c.state.currentToolCallName = tc.Function.Name
				c.state.toolCallArgs.Reset()
				// Gemini function call format
				functionCall := map[string]interface{}{
					"name": tc.Function.Name,
					"args": map[string]interface{}{},
				}
				funcCallJSON, _ := json.Marshal(functionCall)
				return []byte(`{"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"functionCall":` + string(funcCallJSON) + `}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n")
			} else if tc.Function.Arguments != "" {
				// Tool call arguments - need to accumulate and emit as complete
				c.state.toolCallArgs.WriteString(tc.Function.Arguments)
				// For streaming, we emit partial arguments as JSON
				return []byte(`{"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"`+c.state.currentToolCallName+`","args":`+tc.Function.Arguments+`}}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n")
			}
		}
		return nil

	// Finish reason
	case event.Choices[0].FinishReason != "":
		c.state.hasFinished = true
		finishReason := event.Choices[0].FinishReason

		var geminiReason string
		switch finishReason {
		case "stop":
			geminiReason = "STOP"
		case "length":
			geminiReason = "MAX_TOKENS"
		}

		if geminiReason != "" {
			return []byte(`{"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[]},"finishReason":"` + geminiReason + `"}]}}` + "\n\n")
		}
		return nil
	}

	return nil
}

func (c *chatToGeminiConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}}` + "\n\n")
	}
	return nil
}

func (c *chatToGeminiConverter) Reset() {
	c.state.toolCallArgs.Reset()
	c.state.currentToolCallID = ""
	c.state.currentToolCallName = ""
	c.state.hasStarted = false
	c.state.hasFinished = false
	c.state.model = ""

	// Return to pool
	chatToGeminiPool.Put(c.state)
	c.state = nil
}

func init() {
	Register(FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: convert.FormatGemini}, newChatToGeminiConverter)
}