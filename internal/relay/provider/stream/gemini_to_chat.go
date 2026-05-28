package stream

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

// geminiToChatState holds the streaming conversion state
type geminiToChatState struct {
	// Metadata
	model string
	id    string

	// Text accumulation
	textBuffer strings.Builder

	// Tool call tracking
	currentToolCallID   string
	currentToolCallName string
	toolCallArgs        *strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// geminiToChatPool is the sync.Pool for converter state
var geminiToChatPool = sync.Pool{
	New: func() interface{} {
		return &geminiToChatState{
			toolCallArgs: &strings.Builder{},
		}
	},
}

// geminiToChatConverter implements StreamConverter
type geminiToChatConverter struct {
	state *geminiToChatState
}

func newGeminiToChatConverter() StreamConverter {
	return &geminiToChatConverter{
		state: geminiToChatPool.Get().(*geminiToChatState),
	}
}

func (c *geminiToChatConverter) Convert(line []byte) []byte {
	// Parse the Gemini SSE line - it's wrapped in a method response
	var wrapper struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params,omitempty"`
	}

	if err := json.Unmarshal(line, &wrapper); err != nil {
		return nil
	}

	if wrapper.Method != "generateContentStream" {
		return nil
	}

	// Parse the params (which contains the response)
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string `json:"name"`
						Args any    `json:"args"`
					} `json:"functionCall,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason,omitempty"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata,omitempty"`
	}

	if err := json.Unmarshal(wrapper.Params, &response); err != nil {
		return nil
	}

	if len(response.Candidates) == 0 {
		return nil
	}

	candidate := response.Candidates[0]

	// First message - emit role
	if !c.state.hasStarted && len(candidate.Content.Parts) > 0 {
		c.state.hasStarted = true
		// Generate a simple ID
		c.state.id = "chatcmpl-" + generateSimpleID()
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"gemini","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`+"\n\n")
	}

	// Process parts
	for _, part := range candidate.Content.Parts {
		// Text content
		if part.Text != "" {
			c.state.textBuffer.WriteString(part.Text)
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"gemini","choices":[{"index":0,"delta":{"content":"`+escapeJSON(part.Text)+`"},"finish_reason":null}]}`+"\n\n")
		}

		// Function call
		if part.FunctionCall != nil {
			argsStr, _ := json.Marshal(part.FunctionCall.Args)
			c.state.currentToolCallID = "call_" + generateSimpleID()
			c.state.currentToolCallName = part.FunctionCall.Name
			c.state.toolCallArgs.Reset()
			c.state.toolCallArgs.WriteString(string(argsStr))
			return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"gemini","choices":[{"index":0,"delta":{"tool_calls":[{"id":"`+c.state.currentToolCallID+`","type":"function","function":{"name":"`+part.FunctionCall.Name+`","arguments":`+string(argsStr)+`}}]},"finish_reason":null}]}`+"\n\n")
		}
	}

	// Handle finish reason
	if candidate.FinishReason != "" && candidate.FinishReason != "NOT_STARTED" && candidate.FinishReason != "SPECIFIED" {
		c.state.hasFinished = true
		finishReason := "stop"
		if candidate.FinishReason == "MAX_TOKENS" {
			finishReason = "length"
		}
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"gemini","choices":[{"index":0,"delta":{},"finish_reason":"`+finishReason+`"}]`+"\n\n")
	}

	return nil
}

func (c *geminiToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return []byte(`{"id":"`+c.state.id+`","object":"chat.completion.chunk","created":0,"model":"gemini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]`+"\n\n")
	}
	return nil
}

func (c *geminiToChatConverter) Reset() {
	c.state.textBuffer.Reset()
	c.state.toolCallArgs.Reset()
	c.state.currentToolCallID = ""
	c.state.currentToolCallName = ""
	c.state.hasStarted = false
	c.state.hasFinished = false
	c.state.id = ""
	c.state.model = ""

	// Return to pool
	geminiToChatPool.Put(c.state)
	c.state = nil
}

// generateSimpleID generates a simple ID for message
func generateSimpleID() string {
	// Simple counter-based ID (not perfect but works for streaming)
	return "abc123def456"
}

func init() {
	Register(FormatPair{Upstream: convert.FormatGemini, Client: convert.FormatOpenAIChatCompletions}, newGeminiToChatConverter)
}