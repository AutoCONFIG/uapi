package schema

import "encoding/json"

// MessageContent handles polymorphic content (bare string vs array of ContentPart).
type MessageContent struct {
	Text  *string       // bare string content
	Parts []ContentPart // array content
}

type ContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *string         `json:"image_url,omitempty"`
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mime_type,omitempty"`
	Refusal  string          `json:"refusal,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolChoice struct {
	Type     string `json:"type,omitempty"`
	Function string `json:"function,omitempty"`
}

type Usage struct {
	PromptTokens             int                    `json:"prompt_tokens"`
	CompletionTokens         int                    `json:"completion_tokens"`
	TotalTokens              int                    `json:"total_tokens"`
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens,omitempty"`
	PromptTokensDetails      map[string]interface{} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails  map[string]interface{} `json:"completion_tokens_details,omitempty"`
}

type ReasoningConfig struct {
	Effort    string          `json:"effort,omitempty"`
	MaxTokens *int            `json:"max_tokens,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}
