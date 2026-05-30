package schema

// GeminiCLIRequest represents a Google Gemini CLI request wrapper.
type GeminiCLIRequest struct {
	Model              string        `json:"model"`
	Project            string        `json:"project,omitempty"`
	UserPromptID       string        `json:"user_prompt_id,omitempty"`
	EnabledCreditTypes []string      `json:"enabled_credit_types,omitempty"`
	UserAgent          string        `json:"userAgent,omitempty"`
	Request            GeminiRequest `json:"request"`
	RequestType        string        `json:"requestType,omitempty"`
	RequestID          string        `json:"requestId,omitempty"`
	SessionID          string        `json:"sessionId,omitempty"`
}

// GeminiCLIResponse represents a Google Gemini CLI response wrapper.
type GeminiCLIResponse struct {
	Response GeminiResponse `json:"response"`
}
