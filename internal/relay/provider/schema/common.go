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

	known := map[string]bool{
		"type": true, "text": true, "image_url": true, "detail": true,
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
}

type ToolFunction struct {
	Name        string          `json:"name"`
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
