package ir

import "encoding/json"

type Usage struct {
	InputTokens         int                        `json:"input_tokens,omitempty"`
	OutputTokens        int                        `json:"output_tokens,omitempty"`
	TotalTokens         int                        `json:"total_tokens,omitempty"`
	PromptTokens        int                        `json:"prompt_tokens,omitempty"`
	CompletionTokens    int                        `json:"completion_tokens,omitempty"`
	CacheReadTokens     int                        `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens    int                        `json:"cache_write_tokens,omitempty"`
	CacheCreationTokens int                        `json:"cache_creation_tokens,omitempty"`
	ReasoningTokens     int                        `json:"reasoning_tokens,omitempty"`
	AudioTokens         int                        `json:"audio_tokens,omitempty"`
	ImageTokens         int                        `json:"image_tokens,omitempty"`
	TextTokens          int                        `json:"text_tokens,omitempty"`
	ToolTokens          int                        `json:"tool_tokens,omitempty"`
	BillingTokens       int                        `json:"billing_tokens,omitempty"`
	Estimated           bool                       `json:"estimated,omitempty"`
	InputTokenDetails   map[string]json.RawMessage `json:"input_token_details,omitempty"`
	OutputTokenDetails  map[string]json.RawMessage `json:"output_token_details,omitempty"`
	Native              NativeEnvelope             `json:"native,omitempty"`
}
