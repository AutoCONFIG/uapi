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
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if p.InlineData == nil {
			p.InlineData = geminiBlobFromRaw(raw["inline_data"])
		}
		if p.FileData == nil {
			p.FileData = geminiFileDataFromRaw(raw["file_data"])
		}
		if p.FunctionCall == nil {
			p.FunctionCall = geminiFuncCallFromRaw(raw["function_call"])
		}
		if p.FunctionResponse == nil {
			p.FunctionResponse = geminiFuncResponseFromRaw(raw["function_response"])
		}
		if p.ThoughtSignature == "" {
			p.ThoughtSignature = rawStringField(raw["thought_signature"])
		}
	}
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

func geminiBlobFromRaw(raw json.RawMessage) *GeminiBlob {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var blob struct {
		MimeTypeCamel string `json:"mimeType"`
		MimeTypeSnake string `json:"mime_type"`
		Data          string `json:"data"`
	}
	if err := json.Unmarshal(raw, &blob); err != nil {
		return nil
	}
	if blob.MimeTypeCamel == "" {
		blob.MimeTypeCamel = blob.MimeTypeSnake
	}
	if blob.MimeTypeCamel == "" && blob.Data == "" {
		return nil
	}
	return &GeminiBlob{MimeType: blob.MimeTypeCamel, Data: blob.Data}
}

// GeminiFuncCall represents a function call in a Gemini part.
type GeminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

func geminiFuncCallFromRaw(raw json.RawMessage) *GeminiFuncCall {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var call GeminiFuncCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil
	}
	if call.Name == "" && len(call.Args) == 0 {
		return nil
	}
	return &call
}

// GeminiFuncResponse represents a function response in a Gemini part.
type GeminiFuncResponse struct {
	Name         string                     `json:"name"`
	Response     json.RawMessage            `json:"response"`
	ID           string                     `json:"id,omitempty"`
	WillContinue *bool                      `json:"willContinue,omitempty"`
	Scheduling   string                     `json:"scheduling,omitempty"`
	Parts        json.RawMessage            `json:"parts,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

func (r *GeminiFuncResponse) UnmarshalJSON(data []byte) error {
	type Alias GeminiFuncResponse
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = GeminiFuncResponse(a)
	return unmarshalExtra(data, r, &r.Extra)
}

func (r GeminiFuncResponse) MarshalJSON() ([]byte, error) {
	type Alias GeminiFuncResponse
	return marshalExtra(Alias(r), r.Extra)
}

func geminiFuncResponseFromRaw(raw json.RawMessage) *GeminiFuncResponse {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var resp GeminiFuncResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	if resp.Name == "" && len(resp.Response) == 0 {
		return nil
	}
	return &resp
}

// GeminiFileData represents a file reference in a Gemini part.
type GeminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

func geminiFileDataFromRaw(raw json.RawMessage) *GeminiFileData {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var file struct {
		MimeTypeCamel string `json:"mimeType"`
		MimeTypeSnake string `json:"mime_type"`
		FileURICamel  string `json:"fileUri"`
		FileURISnake  string `json:"file_uri"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil
	}
	if file.MimeTypeCamel == "" {
		file.MimeTypeCamel = file.MimeTypeSnake
	}
	if file.FileURICamel == "" {
		file.FileURICamel = file.FileURISnake
	}
	if file.MimeTypeCamel == "" && file.FileURICamel == "" {
		return nil
	}
	return &GeminiFileData{MimeType: file.MimeTypeCamel, FileURI: file.FileURICamel}
}

func rawStringField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
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
	Candidates     []GeminiCandidate          `json:"candidates,omitempty"`
	UsageMetadata  *GeminiUsageMetadata       `json:"usageMetadata,omitempty"`
	ModelVersion   string                     `json:"modelVersion,omitempty"`
	PromptFeedback *GeminiPromptFeedback      `json:"promptFeedback,omitempty"`
	Extra          map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *GeminiResponse) UnmarshalJSON(data []byte) error {
	type Alias GeminiResponse
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = GeminiResponse(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r GeminiResponse) MarshalJSON() ([]byte, error) {
	type Alias GeminiResponse
	return marshalExtra(Alias(r), r.Extra)
}

// GeminiCandidate represents a single candidate in a Gemini response.
type GeminiCandidate struct {
	Content       *GeminiContent             `json:"content,omitempty"`
	FinishReason  string                     `json:"finishReason,omitempty"`
	FinishMessage string                     `json:"finishMessage,omitempty"`
	Index         int                        `json:"index,omitempty"`
	SafetyRatings json.RawMessage            `json:"safetyRatings,omitempty"`
	Extra         map[string]json.RawMessage `json:"-"`
}

func (c *GeminiCandidate) UnmarshalJSON(data []byte) error {
	type Alias GeminiCandidate
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*c = GeminiCandidate(a)
	return unmarshalExtra(data, c, &c.Extra)
}

func (c GeminiCandidate) MarshalJSON() ([]byte, error) {
	type Alias GeminiCandidate
	return marshalExtra(Alias(c), c.Extra)
}

type GeminiPromptFeedback struct {
	BlockReason        string                     `json:"blockReason,omitempty"`
	BlockReasonMessage string                     `json:"blockReasonMessage,omitempty"`
	SafetyRatings      json.RawMessage            `json:"safetyRatings,omitempty"`
	Extra              map[string]json.RawMessage `json:"-"`
}

func (p *GeminiPromptFeedback) UnmarshalJSON(data []byte) error {
	type Alias GeminiPromptFeedback
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*p = GeminiPromptFeedback(a)
	return unmarshalExtra(data, p, &p.Extra)
}

func (p GeminiPromptFeedback) MarshalJSON() ([]byte, error) {
	type Alias GeminiPromptFeedback
	return marshalExtra(Alias(p), p.Extra)
}

// GeminiUsageMetadata represents token usage in the Gemini API.
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}
