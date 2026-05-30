package schema

import (
	"encoding/json"
)

// GeminiRequest represents a Google Gemini API request.
type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	Tools             json.RawMessage         `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    json.RawMessage         `json:"safetySettings,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *GeminiRequest) UnmarshalJSON(data []byte) error {
	type Alias GeminiRequest
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = GeminiRequest(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r GeminiRequest) MarshalJSON() ([]byte, error) {
	type Alias GeminiRequest
	return marshalExtra(Alias(r), r.Extra)
}

// GeminiContent represents a content part in a Gemini request.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a single part in a Gemini content message.
type GeminiPart struct {
	Text                string                `json:"text,omitempty"`
	InlineData          *GeminiBlob           `json:"inlineData,omitempty"`
	FunctionCall        *GeminiFuncCall       `json:"functionCall,omitempty"`
	FunctionResponse    *GeminiFuncResponse   `json:"functionResponse,omitempty"`
	FileData            *GeminiFileData       `json:"fileData,omitempty"`
	Thought             bool                  `json:"thought,omitempty"`
	ThoughtSignature    string                `json:"thoughtSignature,omitempty"`
	ExecutableCode      *GeminiExecutableCode `json:"executableCode,omitempty"`
	CodeExecutionResult *GeminiCodeResult     `json:"codeExecutionResult,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (p *GeminiPart) UnmarshalJSON(data []byte) error {
	type Alias GeminiPart
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*p = GeminiPart(a)
	return unmarshalExtra(data, p, &p.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (p GeminiPart) MarshalJSON() ([]byte, error) {
	type Alias GeminiPart
	return marshalExtra(Alias(p), p.Extra)
}

// GeminiBlob represents inline binary data in a Gemini part.
type GeminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiFuncCall represents a function call in a Gemini part.
type GeminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// GeminiFuncResponse represents a function response in a Gemini part.
type GeminiFuncResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// GeminiFileData represents a file reference in a Gemini part.
type GeminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// GeminiExecutableCode represents executable code in a Gemini part.
type GeminiExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// GeminiCodeResult represents the result of code execution in a Gemini part.
type GeminiCodeResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output"`
}

// GeminiToolConfig represents tool configuration in a Gemini request.
type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// GeminiFunctionCallingConfig represents function calling configuration.
type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GeminiGenerationConfig represents generation configuration in a Gemini request.
type GeminiGenerationConfig struct {
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	CandidateCount   *int            `json:"candidateCount,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
	ThinkingConfig   json.RawMessage `json:"thinkingConfig,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (g *GeminiGenerationConfig) UnmarshalJSON(data []byte) error {
	type Alias GeminiGenerationConfig
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*g = GeminiGenerationConfig(a)
	return unmarshalExtra(data, g, &g.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (g GeminiGenerationConfig) MarshalJSON() ([]byte, error) {
	type Alias GeminiGenerationConfig
	return marshalExtra(Alias(g), g.Extra)
}

// GeminiResponse represents a Google Gemini API response.
type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// GeminiCandidate represents a single candidate in a Gemini response.
type GeminiCandidate struct {
	Content      *GeminiContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
	Index        int            `json:"index,omitempty"`
}

// GeminiUsageMetadata represents token usage in the Gemini API.
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}
