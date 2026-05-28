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
	case c.state.model == "" && event.Model != "" && deltaData.Role != "" && deltaData.Content == "" && len(deltaData.ToolCalls) == 0:
		c.state.model = event.Model
		c.state.hasStarted = true

		return nil

	// Content delta
	case deltaData.Content != "":
		if c.state.model == "" {
			c.state.model = event.Model
		}
		// Gemini wrapped format: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"..."}]}}]}}
		return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": deltaData.Content}}}, "finishReason": "NOT_STARTED"}}}})

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
				var functionCallValue interface{}
				if err := json.Unmarshal(funcCallJSON, &functionCallValue); err != nil {
					return nil
				}
				return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"functionCall": functionCallValue}}}, "finishReason": "NOT_STARTED"}}}})
			} else if tc.Function.Arguments != "" {
				// Tool call arguments - need to accumulate and emit as complete
				c.state.toolCallArgs.WriteString(tc.Function.Arguments)
				// For streaming, we emit partial arguments as JSON
				var args interface{}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					return nil
				}
				return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"functionCall": map[string]interface{}{"name": c.state.currentToolCallName, "args": args}}}}, "finishReason": "NOT_STARTED"}}}})
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
			return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{}}, "finishReason": geminiReason}}}})
		}
		return nil
	}

	return nil
}

func (c *chatToGeminiConverter) Done() []byte {
	if !c.state.hasFinished {
		return sseJSON(map[string]interface{}{"method": "generateContentStream", "params": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{}}, "finishReason": "STOP"}}}})
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
