package schema

import (
	"encoding/json"
)

// AnthropicRequest represents an Anthropic Messages API request.
type AnthropicRequest struct {
	Model         string               `json:"model"`
	Messages      []AnthropicMessage   `json:"messages"`
	MaxTokens     int                  `json:"max_tokens"`
	System        json.RawMessage      `json:"system,omitempty"` // string or []ContentBlock
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         json.RawMessage      `json:"tools,omitempty"`
	ToolChoice    json.RawMessage      `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage      `json:"thinking,omitempty"`
	Metadata      json.RawMessage      `json:"metadata,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *AnthropicRequest) UnmarshalJSON(data []byte) error {
	type Alias AnthropicRequest
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = AnthropicRequest(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r AnthropicRequest) MarshalJSON() ([]byte, error) {
	return marshalExtra(r, r.Extra)
}

// AnthropicMessage represents a single message in an Anthropic request.
type AnthropicMessage struct {
	Role    string                 `json:"role"`
	Content []AnthropicContentBlock `json:"content"`
}

// AnthropicContentBlock represents a content block in an Anthropic message.
type AnthropicContentBlock struct {
	Type       string                `json:"type"`
	Text       string                `json:"text,omitempty"`
	Input      json.RawMessage       `json:"input,omitempty"`
	ID         string                `json:"id,omitempty"`
	Name       string                `json:"name,omitempty"`
	ToolUseID  string                `json:"tool_use_id,omitempty"`
	ContentStr string                `json:"content,omitempty"`
	IsError    bool                  `json:"is_error,omitempty"`
	Source     *AnthropicImageSource `json:"source,omitempty"`
	Thinking   string                `json:"thinking,omitempty"`
	Signature  string                `json:"signature,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (b *AnthropicContentBlock) UnmarshalJSON(data []byte) error {
	type Alias AnthropicContentBlock
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*b = AnthropicContentBlock(a)
	return unmarshalExtra(data, b, &b.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (b AnthropicContentBlock) MarshalJSON() ([]byte, error) {
	return marshalExtra(b, b.Extra)
}

// AnthropicImageSource represents an image source in an Anthropic content block.
type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// AnthropicResponse represents an Anthropic Messages API response.
type AnthropicResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                 `json:"model"`
	StopReason   string                 `json:"stop_reason,omitempty"`
	StopSequence string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage         `json:"usage"`
}

// AnthropicUsage represents token usage in the Anthropic API.
type AnthropicUsage struct {
	InputTokens               int `json:"input_tokens"`
	OutputTokens              int `json:"output_tokens"`
	CacheCreationInputTokens  int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens      int `json:"cache_read_input_tokens,omitempty"`
}
