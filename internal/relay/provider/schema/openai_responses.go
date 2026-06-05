package schema

import (
	"encoding/json"
)

// OpenAIResponsesRequest represents an OpenAI Responses API request.
type OpenAIResponsesRequest struct {
	Model              string          `json:"model"`
	Input              ResponsesInput  `json:"input"`
	Instructions       string          `json:"instructions,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Truncation         string          `json:"truncation,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls  bool            `json:"parallel_tool_calls,omitempty"`
	Reasoning          json.RawMessage `json:"reasoning,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
	StreamOptions      json.RawMessage `json:"stream_options,omitempty"`
	ServiceTier        string          `json:"service_tier,omitempty"`
	Store              bool            `json:"store,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	User               string          `json:"user,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Include            json.RawMessage `json:"include,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (r *OpenAIResponsesRequest) UnmarshalJSON(data []byte) error {
	type Alias OpenAIResponsesRequest
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = OpenAIResponsesRequest(a)
	return unmarshalExtra(data, r, &r.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (r OpenAIResponsesRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAIResponsesRequest
	return marshalExtra(Alias(r), r.Extra)
}

// ResponsesInput is a polymorphic type: a bare string or an array of
// ResponsesInputItem.
type ResponsesInput struct {
	Text  *string              // bare string form
	Items []ResponsesInputItem // array form
}

// MarshalJSON emits a bare string if Text is set, or an array if Items is set.
func (in ResponsesInput) MarshalJSON() ([]byte, error) {
	if in.Text != nil {
		return json.Marshal(*in.Text)
	}
	if len(in.Items) > 0 {
		return json.Marshal(in.Items)
	}
	return json.Marshal(nil)
}

// UnmarshalJSON tries bare string first, then []ResponsesInputItem.
func (in *ResponsesInput) UnmarshalJSON(data []byte) error {
	trimmed := trimNull(data)
	if trimmed == nil {
		in.Text = nil
		in.Items = nil
		return nil
	}

	// Try bare string.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		in.Text = &s
		in.Items = nil
		return nil
	}

	// Try array of ResponsesInputItem.
	var items []ResponsesInputItem
	if err := json.Unmarshal(data, &items); err == nil {
		in.Text = nil
		in.Items = items
		return nil
	}

	return errNotStringOrArray
}

// ResponsesInputItem represents a single item in the Responses API input array.
type ResponsesInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   MessageContent  `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
	OutputRaw json.RawMessage `json:"-"`
	ID        string          `json:"id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Phase     string          `json:"phase,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
	Raw   json.RawMessage            `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (item *ResponsesInputItem) UnmarshalJSON(data []byte) error {
	type Alias ResponsesInputItem
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	outputRaw := append(json.RawMessage(nil), raw["output"]...)
	delete(raw, "output")
	stripped, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var a Alias
	if err := json.Unmarshal(stripped, &a); err != nil {
		return err
	}
	*item = ResponsesInputItem(a)
	if len(outputRaw) > 0 {
		item.OutputRaw = outputRaw
		if err := json.Unmarshal(outputRaw, &item.Output); err != nil {
			item.Output = string(outputRaw)
		}
	}
	item.Raw = append(json.RawMessage(nil), data...)
	return unmarshalExtra(data, item, &item.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (item ResponsesInputItem) MarshalJSON() ([]byte, error) {
	type Alias ResponsesInputItem
	return marshalExtra(Alias(item), item.Extra)
}

// OpenAIResponsesResponse represents an OpenAI Responses API response.
type OpenAIResponsesResponse struct {
	ID        string                `json:"id"`
	Object    string                `json:"object"`
	CreatedAt int64                 `json:"created_at"`
	Model     string                `json:"model"`
	Output    []ResponsesOutputItem `json:"output"`
	Usage     *ResponsesUsage       `json:"usage,omitempty"`
	Status    string                `json:"status,omitempty"`
	Metadata  json.RawMessage       `json:"metadata,omitempty"`
}

// ResponsesOutputItem represents a single item in the Responses API output array.
type ResponsesOutputItem struct {
	Type      string        `json:"type"`
	ID        string        `json:"id,omitempty"`
	Role      string        `json:"role,omitempty"`
	Content   []ContentPart `json:"content,omitempty"`
	Status    string        `json:"status,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures unknown top-level keys into Extra.
func (item *ResponsesOutputItem) UnmarshalJSON(data []byte) error {
	type Alias ResponsesOutputItem
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*item = ResponsesOutputItem(a)
	return unmarshalExtra(data, item, &item.Extra)
}

// MarshalJSON includes Extra fields in the output.
func (item ResponsesOutputItem) MarshalJSON() ([]byte, error) {
	type Alias ResponsesOutputItem
	return marshalExtra(Alias(item), item.Extra)
}

// ResponsesUsage represents token usage in the Responses API.
type ResponsesUsage struct {
	InputTokens              int                    `json:"input_tokens"`
	OutputTokens             int                    `json:"output_tokens"`
	TotalTokens              int                    `json:"total_tokens"`
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens,omitempty"`
	PromptCacheHitTokens     int                    `json:"prompt_cache_hit_tokens,omitempty"`
	InputTokensDetails       map[string]interface{} `json:"input_tokens_details,omitempty"`
}
