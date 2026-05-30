package schema

import "encoding/json"

// MessageContent handles polymorphic content (bare string vs array of ContentPart).
type MessageContent struct {
	Text  *string       // bare string content
	Parts []ContentPart // array content
}

type ContentPart struct {
	Type        string                     `json:"type"`
	Text        string                     `json:"text,omitempty"`
	ImageURL    *string                    `json:"image_url,omitempty"`
	ImageDetail string                     `json:"detail,omitempty"`
	FileData    string                     `json:"file_data,omitempty"`
	FileURL     string                     `json:"file_url,omitempty"`
	FileID      string                     `json:"file_id,omitempty"`
	Filename    string                     `json:"filename,omitempty"`
	FileType    string                     `json:"file_type,omitempty"`
	Data        string                     `json:"data,omitempty"`
	MimeType    string                     `json:"mime_type,omitempty"`
	Refusal     string                     `json:"refusal,omitempty"`
	Extra       map[string]json.RawMessage `json:"-"`
}

func (p *ContentPart) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var out ContentPart
	if v, ok := raw["type"]; ok {
		_ = json.Unmarshal(v, &out.Type)
	}
	if v, ok := raw["text"]; ok {
		_ = json.Unmarshal(v, &out.Text)
	}
	if v, ok := raw["data"]; ok {
		_ = json.Unmarshal(v, &out.Data)
	}
	if v, ok := raw["mime_type"]; ok {
		_ = json.Unmarshal(v, &out.MimeType)
	}
	if v, ok := raw["detail"]; ok {
		_ = json.Unmarshal(v, &out.ImageDetail)
	}
	if v, ok := raw["refusal"]; ok {
		_ = json.Unmarshal(v, &out.Refusal)
	}
	if v, ok := raw["file_data"]; ok {
		_ = json.Unmarshal(v, &out.FileData)
	}
	if v, ok := raw["file_url"]; ok {
		_ = json.Unmarshal(v, &out.FileURL)
	}
	if v, ok := raw["file_id"]; ok {
		_ = json.Unmarshal(v, &out.FileID)
	}
	if v, ok := raw["filename"]; ok {
		_ = json.Unmarshal(v, &out.Filename)
	}
	if v, ok := raw["file_type"]; ok {
		_ = json.Unmarshal(v, &out.FileType)
	}
	if v, ok := raw["image_url"]; ok {
		var imageURL string
		if err := json.Unmarshal(v, &imageURL); err == nil {
			out.ImageURL = &imageURL
		} else {
			var image struct {
				URL    string  `json:"url"`
				Detail *string `json:"detail,omitempty"`
			}
			if err := json.Unmarshal(v, &image); err == nil {
				if image.URL != "" {
					out.ImageURL = &image.URL
				}
				if image.Detail != nil {
					out.ImageDetail = *image.Detail
				}
			}
			out.Extra = setPartExtra(out.Extra, "image_url", v)
		}
	}
	if v, ok := raw["file"]; ok {
		var file struct {
			FileData string `json:"file_data,omitempty"`
			FileURL  string `json:"file_url,omitempty"`
			FileID   string `json:"file_id,omitempty"`
			Filename string `json:"filename,omitempty"`
			FileType string `json:"file_type,omitempty"`
		}
		if err := json.Unmarshal(v, &file); err == nil {
			out.FileData = firstNonEmpty(out.FileData, file.FileData)
			out.FileURL = firstNonEmpty(out.FileURL, file.FileURL)
			out.FileID = firstNonEmpty(out.FileID, file.FileID)
			out.Filename = firstNonEmpty(out.Filename, file.Filename)
			out.FileType = firstNonEmpty(out.FileType, file.FileType)
		}
	}

	known := map[string]bool{
		"type": true, "text": true, "image_url": true, "detail": true,
		"file": true, "file_data": true, "file_url": true, "file_id": true,
		"filename": true, "file_type": true,
		"data": true, "mime_type": true, "refusal": true,
	}
	for k, v := range raw {
		if !known[k] {
			out.Extra = setPartExtra(out.Extra, k, v)
		}
	}

	*p = out
	return nil
}

func (p ContentPart) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{})
	if p.Type != "" {
		out["type"] = p.Type
	}
	if p.Text != "" {
		out["text"] = p.Text
	}
	if p.ImageURL != nil {
		out["image_url"] = *p.ImageURL
	}
	if p.ImageDetail != "" {
		out["detail"] = p.ImageDetail
	}
	if p.Type == "file" {
		file := make(map[string]interface{})
		if p.FileData != "" {
			file["file_data"] = p.FileData
		}
		if p.FileURL != "" {
			file["file_url"] = p.FileURL
		}
		if p.FileID != "" {
			file["file_id"] = p.FileID
		}
		if p.Filename != "" {
			file["filename"] = p.Filename
		}
		if p.FileType != "" {
			file["file_type"] = p.FileType
		}
		if len(file) > 0 {
			out["file"] = file
		}
	} else {
		if p.FileData != "" {
			out["file_data"] = p.FileData
		}
		if p.FileURL != "" {
			out["file_url"] = p.FileURL
		}
		if p.FileID != "" {
			out["file_id"] = p.FileID
		}
		if p.Filename != "" {
			out["filename"] = p.Filename
		}
		if p.FileType != "" {
			out["file_type"] = p.FileType
		}
	}
	if p.Data != "" {
		out["data"] = p.Data
	}
	if p.MimeType != "" {
		out["mime_type"] = p.MimeType
	}
	if p.Refusal != "" {
		out["refusal"] = p.Refusal
	}
	for k, v := range p.Extra {
		out[k] = v
	}
	return json.Marshal(out)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func setPartExtra(extra map[string]json.RawMessage, key string, value json.RawMessage) map[string]json.RawMessage {
	if extra == nil {
		extra = make(map[string]json.RawMessage)
	}
	extra[key] = append(json.RawMessage(nil), value...)
	return extra
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
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Function    *ToolFunction   `json:"function,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

func (t *Tool) UnmarshalJSON(data []byte) error {
	type Alias Tool
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*t = Tool(a)
	return unmarshalExtra(data, t, &t.Extra)
}

func (t Tool) MarshalJSON() ([]byte, error) {
	type Alias Tool
	return marshalExtra(Alias(t), t.Extra)
}

func (f *ToolFunction) UnmarshalJSON(data []byte) error {
	type Alias ToolFunction
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*f = ToolFunction(a)
	return unmarshalExtra(data, f, &f.Extra)
}

func (f ToolFunction) MarshalJSON() ([]byte, error) {
	type Alias ToolFunction
	return marshalExtra(Alias(f), f.Extra)
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
