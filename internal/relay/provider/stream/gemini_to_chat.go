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
	data, ok := sseData(line)
	if !ok || data == "[DONE]" {
		return nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return nil
	}
	if methodRaw, ok := root["method"]; ok {
		var method string
		if json.Unmarshal(methodRaw, &method) == nil && method == "generateContentStream" {
			return c.convertResponse(root["params"])
		}
	}
	if responseRaw, ok := root["response"]; ok {
		var responses []json.RawMessage
		if json.Unmarshal(responseRaw, &responses) == nil {
			var out []byte
			for _, response := range responses {
				out = append(out, c.convertResponse(response)...)
			}
			return out
		}
		return c.convertResponse(responseRaw)
	}
	return c.convertResponse([]byte(data))
}

func (c *geminiToChatConverter) convertResponse(body []byte) []byte {
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text             string `json:"text"`
					Thought          bool   `json:"thought,omitempty"`
					ThoughtSignature string `json:"thoughtSignature,omitempty"`
					FunctionCall     *struct {
						Name string `json:"name"`
						Args any    `json:"args"`
					} `json:"functionCall,omitempty"`
					FunctionResponse *struct {
						Name     string          `json:"name"`
						Response json.RawMessage `json:"response"`
					} `json:"functionResponse,omitempty"`
					ExecutableCode *struct {
						Language string `json:"language"`
						Code     string `json:"code"`
					} `json:"executableCode,omitempty"`
					CodeExecutionResult *struct {
						Output string `json:"output"`
					} `json:"codeExecutionResult,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason,omitempty"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata,omitempty"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}

	if len(response.Candidates) == 0 {
		if response.UsageMetadata != nil {
			return chatChunk(c.state.id, "gemini", map[string]interface{}{}, nil, map[string]interface{}{
				"prompt_tokens":     response.UsageMetadata.PromptTokenCount,
				"completion_tokens": response.UsageMetadata.CandidatesTokenCount,
				"total_tokens":      response.UsageMetadata.PromptTokenCount + response.UsageMetadata.CandidatesTokenCount,
			})
		}
		return nil
	}

	candidate := response.Candidates[0]
	var out []byte

	// First message - emit role
	if !c.state.hasStarted && len(candidate.Content.Parts) > 0 {
		c.state.hasStarted = true
		c.state.id = randomID("chatcmpl-")
		out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{"role": "assistant"}, nil, nil)...)
	}

	// Process parts
	for _, part := range candidate.Content.Parts {
		// Text content
		if part.Text != "" {
			c.state.textBuffer.WriteString(part.Text)
			key := "content"
			if part.Thought {
				out = append(out, chatChunk(c.state.id, "gemini", reasoningTextDelta(part.Text, 0, part.ThoughtSignature), nil, nil)...)
				continue
			}
			out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{key: part.Text}, nil, nil)...)
			continue
		}
		if part.ThoughtSignature != "" {
			out = append(out, chatChunk(c.state.id, "gemini", reasoningEncryptedDelta(0, part.ThoughtSignature), nil, nil)...)
			continue
		}

		// Function call
		if part.FunctionCall != nil {
			argsStr, _ := json.Marshal(part.FunctionCall.Args)
			c.state.currentToolCallID = randomID("call_")
			c.state.currentToolCallName = part.FunctionCall.Name
			c.state.toolCallArgs.Reset()
			c.state.toolCallArgs.WriteString(string(argsStr))
			out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{"tool_calls": []interface{}{
				map[string]interface{}{"index": 0, "id": c.state.currentToolCallID, "type": "function", "function": map[string]interface{}{"name": part.FunctionCall.Name, "arguments": string(argsStr)}},
			}}, nil, nil)...)
		}
		if part.FunctionResponse != nil {
			out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{"content": geminiFunctionResponseText(part.FunctionResponse.Response)}, nil, nil)...)
		}
		if part.ExecutableCode != nil {
			out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{"content": "```" + part.ExecutableCode.Language + "\n" + part.ExecutableCode.Code + "\n```"}, nil, nil)...)
		}
		if part.CodeExecutionResult != nil {
			out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{"content": part.CodeExecutionResult.Output}, nil, nil)...)
		}
	}

	// Handle finish reason
	if candidate.FinishReason != "" && candidate.FinishReason != "NOT_STARTED" && candidate.FinishReason != "SPECIFIED" {
		c.state.hasFinished = true
		finishReason := "stop"
		if candidate.FinishReason == "MAX_TOKENS" {
			finishReason = "length"
		}
		out = append(out, chatChunk(c.state.id, "gemini", map[string]interface{}{}, finishReason, geminiUsage(response.UsageMetadata))...)
	}

	return out
}

func geminiUsage(usage *struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}) map[string]interface{} {
	if usage == nil {
		return nil
	}
	return map[string]interface{}{
		"prompt_tokens":     usage.PromptTokenCount,
		"completion_tokens": usage.CandidatesTokenCount,
		"total_tokens":      usage.PromptTokenCount + usage.CandidatesTokenCount,
	}
}

func (c *geminiToChatConverter) Done() []byte {
	if !c.state.hasFinished {
		return chatChunk(c.state.id, "gemini", map[string]interface{}{}, "stop", nil)
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

func init() {
	Register(FormatPair{Upstream: convert.FormatGemini, Client: convert.FormatOpenAIChatCompletions}, newGeminiToChatConverter)
}

func geminiFunctionResponseText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &response) == nil {
		var parts []string
		for _, part := range response.Content {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	var value interface{}
	if json.Unmarshal(raw, &value) == nil {
		if body, err := json.Marshal(value); err == nil {
			return string(body)
		}
	}
	return string(raw)
}
