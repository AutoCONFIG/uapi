package schema

import (
	"encoding/json"
)

// OpenAIChatRequest represents an OpenAI Chat Completions API request.
type OpenAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       json.RawMessage `json:"stream_options,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"` // string or []string
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	Seed                *int            `json:"seed,omitempty"`
	LogProbs            *bool           `json:"logprobs,omitempty"`
	TopLogProbs         *int            `json:"top_logprobs,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	LogitBias           json.RawMessage `json:"logit_bias,omitempty"`
	Tools               []Tool          `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	ServiceTier         string          `json:"service_tier,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Store               *bool           `json:"store,omitempty"`
	User                string          `json:"user,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *OpenAIChatRequest) UnmarshalJSON(data []byte) error {
	type Alias OpenAIChatRequest
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = OpenAIChatRequest(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r OpenAIChatRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAIChatRequest
	return marshalExtra(Alias(r), r.Extra)
}

// ChatMessage represents a single message in an OpenAI Chat request or response.
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    MessageContent `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Refusal    string         `json:"refusal,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type Alias ChatMessage
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*m = ChatMessage(a)
	return unmarshalExtra(data, m, &m.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type Alias ChatMessage
	return marshalExtra(Alias(m), m.Extra)
}

// OpenAIChatResponse represents an OpenAI Chat Completions API response.
type OpenAIChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *OpenAIChatResponse) UnmarshalJSON(data []byte) error {
	type Alias OpenAIChatResponse
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = OpenAIChatResponse(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r OpenAIChatResponse) MarshalJSON() ([]byte, error) {
	type Alias OpenAIChatResponse
	return marshalExtra(Alias(r), r.Extra)
}

// ChatChoice represents a single choice in a Chat Completions response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (c *ChatChoice) UnmarshalJSON(data []byte) error {
	type Alias ChatChoice
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*c = ChatChoice(a)
	return unmarshalExtra(data, c, &c.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (c ChatChoice) MarshalJSON() ([]byte, error) {
	type Alias ChatChoice
	return marshalExtra(Alias(c), c.Extra)
}
