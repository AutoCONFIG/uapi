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
	toolCallIDs   map[int]string
	toolCallNames map[int]string
	toolCallArgs  map[int]*strings.Builder

	// State flags
	hasStarted  bool
	hasFinished bool
}

// chatToGeminiPool is the sync.Pool for converter state
var chatToGeminiPool = sync.Pool{
	New: func() interface{} {
		return &chatToGeminiState{
			toolCallIDs:   make(map[int]string),
			toolCallNames: make(map[int]string),
			toolCallArgs:  make(map[int]*strings.Builder),
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
		Role             string          `json:"role"`
		Content          string          `json:"content"`
		ReasoningContent string          `json:"reasoning_content"`
		Reasoning        string          `json:"reasoning"`
		ReasoningDetails json.RawMessage `json:"reasoning_details"`
		ToolCalls        []struct {
			Index    int    `json:"index"`
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
		return geminiStreamEvent(deltaData.Content, "NOT_STARTED")

	case deltaData.ReasoningContent != "" || deltaData.Reasoning != "" || len(deltaData.ReasoningDetails) > 0:
		if c.state.model == "" {
			c.state.model = event.Model
		}
		if details := parseChatReasoningDetails(deltaData.ReasoningDetails); len(details) > 0 {
			var out []byte
			for _, detail := range details {
				text := reasoningDetailText(detail)
				sig := reasoningDetailEncrypted(detail)
				if sig == "" {
					sig = reasoningDetailSignature(detail)
				}
				if text != "" {
					out = append(out, geminiThoughtStreamEvent(text, sig, "NOT_STARTED")...)
					continue
				}
				if sig != "" {
					out = append(out, geminiThoughtSignatureStreamEvent(sig, "NOT_STARTED")...)
				}
			}
			return out
		}
		text := deltaData.ReasoningContent
		if text == "" {
			text = deltaData.Reasoning
		}
		return geminiThoughtStreamEvent(text, "", "NOT_STARTED")

	// Tool call start
	case len(deltaData.ToolCalls) > 0:
		var out []byte
		for _, tc := range deltaData.ToolCalls {
			idx := tc.Index
			if tc.ID != "" {
				c.state.toolCallIDs[idx] = tc.ID
			}
			if tc.Function.Name != "" {
				c.state.toolCallNames[idx] = tc.Function.Name
				if c.state.toolCallArgs[idx] == nil {
					c.state.toolCallArgs[idx] = &strings.Builder{}
				}
			}
			if tc.Function.Arguments != "" {
				if c.state.toolCallArgs[idx] == nil {
					c.state.toolCallArgs[idx] = &strings.Builder{}
				}
				c.state.toolCallArgs[idx].WriteString(tc.Function.Arguments)
			}
			if event.Choices[0].FinishReason == "tool_calls" {
				c.state.hasFinished = true
				out = append(out, c.geminiFunctionCallEvent(idx)...)
			}
		}
		return out

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

		if finishReason == "tool_calls" {
			return c.geminiFunctionCallEvents()
		}
		if geminiReason != "" {
			return geminiStreamFinishEvent(geminiReason)
		}
		return nil
	}

	return nil
}

func (c *chatToGeminiConverter) Done() []byte {
	if !c.state.hasFinished {
		return geminiStreamFinishEvent("STOP")
	}
	return nil
}

func (c *chatToGeminiConverter) geminiFunctionCallEvents() []byte {
	var out []byte
	for idx := range c.state.toolCallNames {
		out = append(out, c.geminiFunctionCallEvent(idx)...)
	}
	return out
}

func (c *chatToGeminiConverter) geminiFunctionCallEvent(idx int) []byte {
	name := c.state.toolCallNames[idx]
	if name == "" {
		return nil
	}
	args := map[string]interface{}{}
	if b := c.state.toolCallArgs[idx]; b != nil && b.Len() > 0 {
		var parsed interface{}
		if err := json.Unmarshal([]byte(b.String()), &parsed); err == nil {
			if parsedMap, ok := parsed.(map[string]interface{}); ok {
				args = parsedMap
			} else {
				args = map[string]interface{}{"value": parsed}
			}
		} else {
			args = map[string]interface{}{"arguments": b.String()}
		}
	}
	return sseJSON(map[string]interface{}{
		"method": "generateContentStream",
		"params": map[string]interface{}{
			"candidates": []interface{}{map[string]interface{}{
				"content": map[string]interface{}{
					"parts": []interface{}{map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": name,
							"args": args,
						},
					}},
				},
				"finishReason": "STOP",
			}},
		},
	})
}

func geminiStreamEvent(text string, finishReason string) []byte {
	return sseJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{map[string]interface{}{"text": text}},
				},
				"finishReason": finishReason,
			},
		},
	})
}

func geminiThoughtStreamEvent(text string, thoughtSignature string, finishReason string) []byte {
	part := map[string]interface{}{"text": text, "thought": true}
	if thoughtSignature != "" {
		part["thoughtSignature"] = thoughtSignature
	}
	return sseJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{part},
				},
				"finishReason": finishReason,
			},
		},
	})
}

func geminiThoughtSignatureStreamEvent(thoughtSignature string, finishReason string) []byte {
	return sseJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{map[string]interface{}{"thoughtSignature": thoughtSignature}},
				},
				"finishReason": finishReason,
			},
		},
	})
}

func geminiStreamFinishEvent(finishReason string) []byte {
	return sseJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{},
				},
				"finishReason": finishReason,
			},
		},
	})
}

func (c *chatToGeminiConverter) Reset() {
	for k := range c.state.toolCallIDs {
		delete(c.state.toolCallIDs, k)
	}
	for k := range c.state.toolCallNames {
		delete(c.state.toolCallNames, k)
	}
	for k, v := range c.state.toolCallArgs {
		if v != nil {
			v.Reset()
		}
		delete(c.state.toolCallArgs, k)
	}
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
