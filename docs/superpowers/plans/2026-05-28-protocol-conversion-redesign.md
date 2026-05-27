# Protocol Conversion Layer Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the relay provider conversion layer with strong-typed schema structs, bidirectional methods, and sync.Pool streaming, referencing bifrost's architecture.

**Architecture:** 5 protocol schema structs in `schema/` package → `InternalRequest` (with `Instructions *string`) as intermediate → bidirectional `ToInternal()`/`FromInternal*()` methods → same-format passthrough bypasses Internal round-trip → streaming state machines with sync.Pool.

**Tech Stack:** Go, encoding/json, sync.Pool, fasthttp

**Spec:** `docs/superpowers/specs/2026-05-28-protocol-conversion-redesign.md`

---

## File Structure

### New files to create:

```
internal/relay/provider/schema/
├── common.go              # Shared types: MessageContent, ContentPart, ToolCall, etc.
├── json.go                # Custom JSON marshal/unmarshal for MessageContent
├── openai_chat.go         # OpenAIChatRequest, OpenAIChatMessage, OpenAIChatResponse
├── openai_responses.go    # OpenAIResponsesRequest, ResponsesInputItem, OpenAIResponsesResponse
├── anthropic.go           # AnthropicRequest, AnthropicMessage, AnthropicResponse
├── gemini.go              # GeminiRequest, GeminiContent, GeminiResponse
└── gemini_cli.go          # GeminiCLIRequest (envelope wrapping GeminiRequest)

internal/relay/provider/convert/
├── internal.go            # New InternalRequest, InternalMessage (simplified)
├── openai_chat.go         # OpenAIChatRequest.ToInternal(), InternalToOpenAIChat()
├── openai_responses.go    # OpenAIResponsesRequest.ToInternal(), InternalToOpenAIResponses()
├── anthropic.go           # AnthropicRequest.ToInternal(), InternalToAnthropic()
├── gemini.go              # GeminiRequest.ToInternal(), InternalToGemini()
├── gemini_cli.go          # GeminiCLIRequest.ToInternal(), InternalToGeminiCLI()
├── response_openai.go     # Response conversion for OpenAI Chat/Responses
├── response_anthropic.go  # Response conversion for Anthropic
├── response_gemini.go     # Response conversion for Gemini/GeminiCLI
└── registry.go            # Schema registry + ConvertRequest/ConvertResponse dispatch

internal/relay/provider/stream/
├── converter.go           # StreamConverter interface + FormatPair + registry
├── pool.go                # sync.Pool acquisition/release helpers
├── responses_to_chat.go   # Responses SSE → Chat SSE state machine
├── chat_to_responses.go   # Chat SSE → Responses SSE state machine
├── anthropic_to_chat.go   # Anthropic SSE → Chat SSE state machine
├── chat_to_anthropic.go   # Chat SSE → Anthropic SSE state machine
├── gemini_to_chat.go      # Gemini SSE → Chat SSE state machine
└── chat_to_gemini.go      # Chat SSE → Gemini SSE state machine
```

### Existing files to modify:

```
internal/relay/provider/types.go           # Simplify InternalRequest, add Instructions, add FormatGeminiCLI
internal/relay/provider/convert.go         # Rewrite to use new registry
internal/relay/provider/anthropic/adaptor.go    # Delegate to new convert/
internal/relay/provider/gemini/adaptor.go       # Delegate to new convert/
internal/relay/provider/openai/adaptor.go       # Delegate to new convert/
internal/relay/provider/antigravity/adaptor.go  # Delegate to new convert/ + GeminiCLI
internal/relay/handler.go                       # Same-format passthrough, new streaming dispatch
internal/relay/streaming.go                     # Use stream/ package
```

### Existing files to remove (after migration):

```
internal/relay/provider/anthropic/to_internal.go
internal/relay/provider/anthropic/from_internal.go
internal/relay/provider/anthropic/response_convert.go
internal/relay/provider/anthropic/streaming.go
internal/relay/provider/gemini/to_internal.go
internal/relay/provider/gemini/from_internal.go
internal/relay/provider/gemini/response_convert.go
internal/relay/provider/gemini/streaming.go
internal/relay/provider/openai/to_internal.go
internal/relay/provider/openai/response_convert.go
internal/relay/provider/openai/streaming.go
internal/relay/provider/openai/responses.go
```

---

## Phase 1: Schema Structs (No behavioral changes)

### Task 1: Create schema/common.go — Shared types

**Files:**
- Create: `internal/relay/provider/schema/common.go`
- Create: `internal/relay/provider/schema/json.go`

- [ ] **Step 1: Create schema package with shared types**

```go
// Package schema defines strong-typed request/response structs for each
// supported LLM API protocol. Custom JSON marshal/unmarshal handles the
// content polymorphism (string vs array) that varies across protocols.
package schema

import "encoding/json"

// MessageContent handles the polymorphic content field that can be a
// bare string or an array of ContentPart objects. This follows bifrost's
// pointer-discriminated union pattern.
type MessageContent struct {
	Text  *string       // bare string content: "hello"
	Parts []ContentPart // array content: [{type:"text",text:"hello"},...]
}

// ContentPart represents a single content block in a message.
type ContentPart struct {
	Type     string          `json:"type"`               // text, image_url, input_image, input_text, output_text, etc.
	Text     string          `json:"text,omitempty"`      // for text-type parts
	ImageURL *string         `json:"image_url,omitempty"` // for image_url-type parts
	Data     string          `json:"data,omitempty"`      // for inline data (base64)
	MimeType string          `json:"mime_type,omitempty"` // MIME type for inline data
	Refusal  string          `json:"refusal,omitempty"`   // for refusal-type parts

	// Extra preserves unrecognized keys for lossless round-tripping.
	Extra map[string]json.RawMessage `json:"-"`
}

// ToolCall represents a tool/function call in an assistant message.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`               // usually "function"
	Name     string `json:"name,omitempty"`      // deprecated: use Function.Name
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolResult represents a tool result in a tool message.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// Tool represents a tool definition.
type Tool struct {
	Type        string          `json:"type"`                    // function, etc.
	Name        string          `json:"name,omitempty"`          // for function type
	Description string          `json:"description,omitempty"`   // for function type
	Parameters  json.RawMessage `json:"parameters,omitempty"`    // JSON Schema
}

// ToolChoice represents tool choice configuration.
// Can be "auto", "none", "required", or {"type":"function","function":{"name":"..."}}.
type ToolChoice struct {
	Type     string `json:"type,omitempty"`     // auto, none, required, function
	Function string `json:"function,omitempty"` // function name when type=function
}

// Usage represents token usage information.
type Usage struct {
	PromptTokens             int                    `json:"prompt_tokens"`
	CompletionTokens         int                    `json:"completion_tokens"`
	TotalTokens              int                    `json:"total_tokens"`
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens,omitempty"`
	PromptTokensDetails      map[string]interface{} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails  map[string]interface{} `json:"completion_tokens_details,omitempty"`
}

// ReasoningConfig represents reasoning/thinking configuration.
type ReasoningConfig struct {
	Effort    string          `json:"effort,omitempty"`    // low, medium, high
	MaxTokens *int            `json:"max_tokens,omitempty"`
	Summary   string          `json:"summary,omitempty"`   // auto, concise, detailed, none
	Raw       json.RawMessage `json:"-"`                   // preserve original JSON
}

// ThinkingConfig represents Anthropic extended thinking configuration.
type ThinkingConfig struct {
	Type         string `json:"type"`                    // enabled, disabled
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}
```

- [ ] **Step 2: Create schema/json.go — Custom JSON marshal/unmarshal**

```go
package schema

import (
	"encoding/json"
	"fmt"
)

// MarshalJSON implements custom marshaling for MessageContent.
// Emits a bare string if Text is set, otherwise an array of ContentPart.
func (mc MessageContent) MarshalJSON() ([]byte, error) {
	if mc.Text != nil && mc.Parts != nil {
		return nil, fmt.Errorf("MessageContent: both Text and Parts are set")
	}
	if mc.Text != nil {
		return json.Marshal(*mc.Text)
	}
	if mc.Parts != nil {
		return json.Marshal(mc.Parts)
	}
	return json.Marshal(nil)
}

// UnmarshalJSON implements custom unmarshaling for MessageContent.
// Tries string first, then falls back to array of ContentPart.
func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		mc.Text = nil
		mc.Parts = nil
		return nil
	}
	// Try string first
	var s string
	if json.Unmarshal(data, &s) == nil {
		mc.Text = &s
		mc.Parts = nil
		return nil
	}
	// Try array of ContentPart
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return fmt.Errorf("MessageContent: expected string or array, got: %s", string(data[:min(len(data), 50)]))
	}
	mc.Text = nil
	mc.Parts = parts
	return nil
}

// IsEmpty returns true if the content has no text or parts.
func (mc *MessageContent) IsEmpty() bool {
	if mc == nil {
		return true
	}
	if mc.Text != nil && *mc.Text != "" {
		return false
	}
	return len(mc.Parts) == 0
}

// ExtractText extracts all text from the content, joining multiple text parts.
func (mc *MessageContent) ExtractText() string {
	if mc == nil {
		return ""
	}
	if mc.Text != nil {
		return *mc.Text
	}
	var texts []string
	for _, p := range mc.Parts {
		if p.Type == "text" || p.Type == "input_text" || p.Type == "output_text" {
			texts = append(texts, p.Text)
		}
	}
	result := ""
	for i, t := range texts {
		if i > 0 {
			result += "\n"
		}
		result += t
	}
	return result
}

// SetText creates a MessageContent from a plain string.
func NewTextContent(text string) MessageContent {
	return MessageContent{Text: &text}
}

// NewPartsContent creates a MessageContent from content parts.
func NewPartsContent(parts ...ContentPart) MessageContent {
	return MessageContent{Parts: parts}
}

// TextPart creates a text ContentPart.
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ImageURLPart creates an image_url ContentPart.
func ImageURLPart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &url}
}
```

- [ ] **Step 3: Run go build to verify compilation**

Run: `go build ./internal/relay/provider/schema/...`
Expected: PASS

- [ ] **Step 4: Write and run tests for MessageContent JSON round-trip**

Create `internal/relay/provider/schema/json_test.go`:

```go
package schema

import (
	"encoding/json"
	"testing"
)

func TestMessageContentStringRoundTrip(t *testing.T) {
	original := NewTextContent("hello world")
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"hello world"` {
		t.Fatalf("expected bare string, got: %s", data)
	}
	var parsed MessageContent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Text == nil || *parsed.Text != "hello world" {
		t.Fatalf("expected text='hello world', got: %+v", parsed)
	}
}

func TestMessageContentArrayRoundTrip(t *testing.T) {
	original := NewPartsContent(TextPart("hello"), ImageURLPart("https://example.com/img.png"))
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed MessageContent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parsed.Parts))
	}
	if parsed.Parts[0].Type != "text" || parsed.Parts[0].Text != "hello" {
		t.Fatalf("unexpected first part: %+v", parsed.Parts[0])
	}
}

func TestMessageContentNullRoundTrip(t *testing.T) {
	var original MessageContent
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("expected null, got: %s", data)
	}
	var parsed MessageContent
	if err := json.Unmarshal([]byte("null"), &parsed); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !parsed.IsEmpty() {
		t.Fatalf("expected empty, got: %+v", parsed)
	}
}

func TestMessageContentExtractTextFromString(t *testing.T) {
	mc := NewTextContent("hello")
	if mc.ExtractText() != "hello" {
		t.Fatalf("expected 'hello', got '%s'", mc.ExtractText())
	}
}

func TestMessageContentExtractTextFromParts(t *testing.T) {
	mc := NewPartsContent(TextPart("hello"), TextPart("world"))
	if mc.ExtractText() != "hello\nworld" {
		t.Fatalf("expected 'hello\\nworld', got '%s'", mc.ExtractText())
	}
}
```

Run: `go test ./internal/relay/provider/schema/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/relay/provider/schema/
git commit -m "feat(schema): add shared types and MessageContent JSON polymorphism"
```

---

### Task 2: Create schema/openai_chat.go — OpenAI Chat Completions structs

**Files:**
- Create: `internal/relay/provider/schema/openai_chat.go`

- [ ] **Step 1: Define OpenAI Chat Completions request/response structs**

Key design: All unknown top-level JSON keys are captured into `Extra` for lossless same-format passthrough. Content uses `MessageContent` for string/array polymorphism.

```go
package schema

import "encoding/json"

// OpenAIChatRequest represents an OpenAI Chat Completions API request.
type OpenAIChatRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage   `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int         `json:"max_completion_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	StreamOptions    *json.RawMessage `json:"stream_options,omitempty"`
	Stop             *json.RawMessage `json:"stop,omitempty"` // string or []string
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	Seed             *int64          `json:"seed,omitempty"`
	LogProbs         *bool           `json:"logprobs,omitempty"`
	TopLogProbs      *int            `json:"top_logprobs,omitempty"`
	ResponseFormat   json.RawMessage `json:"response_format,omitempty"`
	LogitBias        json.RawMessage `json:"logit_bias,omitempty"`
	Tools            json.RawMessage `json:"tools,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	ServiceTier      string          `json:"service_tier,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	Store            *bool           `json:"store,omitempty"`
	User             string          `json:"user,omitempty"`

	// Extra captures unrecognized top-level keys for passthrough.
	Extra map[string]json.RawMessage `json:"-"`
}

// ChatMessage represents a single message in Chat Completions.
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    MessageContent `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"` // for role=tool
	Refusal    string         `json:"refusal,omitempty"`
}

// UnmarshalJSON captures unrecognized top-level keys into Extra.
func (r *OpenAIChatRequest) UnmarshalJSON(data []byte) error {
	type Alias OpenAIChatRequest
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	// Known keys that are handled by the struct
	known := map[string]bool{
		"model": true, "messages": true, "max_tokens": true, "max_completion_tokens": true,
		"temperature": true, "top_p": true, "n": true, "stream": true, "stream_options": true,
		"stop": true, "frequency_penalty": true, "presence_penalty": true, "seed": true,
		"logprobs": true, "top_logprobs": true, "response_format": true, "logit_bias": true,
		"tools": true, "tool_choice": true, "parallel_tool_calls": true, "service_tier": true,
		"reasoning_effort": true, "store": true, "user": true,
	}
	// Extract unknown keys into Extra
	r.Extra = make(map[string]json.RawMessage)
	for k, v := range raw {
		if !known[k] {
			r.Extra[k] = v
		}
	}
	// Unmarshal known fields
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*r = OpenAIChatRequest(alias)
	r.Extra = make(map[string]json.RawMessage)
	for k, v := range raw {
		if !known[k] {
			r.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON includes Extra keys in the output.
func (r OpenAIChatRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAIChatRequest
	data, err := json.Marshal(Alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return data, nil
	}
	// Merge Extra into the marshaled object
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	for k, v := range r.Extra {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// OpenAIChatResponse represents an OpenAI Chat Completions API response.
type OpenAIChatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []ChatChoice     `json:"choices"`
	Usage   *Usage           `json:"usage,omitempty"`
}

// ChatChoice represents a single choice in a Chat Completions response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}
```

- [ ] **Step 2: Run go build**

Run: `go build ./internal/relay/provider/schema/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/relay/provider/schema/openai_chat.go
git commit -m "feat(schema): add OpenAI Chat Completions request/response structs"
```

---

### Task 3: Create schema/openai_responses.go — OpenAI Responses API structs

**Files:**
- Create: `internal/relay/provider/schema/openai_responses.go`

- [ ] **Step 1: Define OpenAI Responses API request/response structs**

Key design: `Instructions` is a top-level field (not in messages). `Input` can be a string or array of `ResponsesInputItem`. Tool calls use `function_call` / `function_call_output` item types (not inline in messages).

```go
package schema

import "encoding/json"

// OpenAIResponsesRequest represents an OpenAI Responses API request.
type OpenAIResponsesRequest struct {
	Model            string           `json:"model"`
	Input            ResponsesInput   `json:"input,omitempty"`
	Instructions     json.RawMessage  `json:"instructions,omitempty"` // string or array of messages
	MaxOutputTokens  *int             `json:"max_output_tokens,omitempty"`
	Temperature      *float64         `json:"temperature,omitempty"`
	TopP             *float64         `json:"top_p,omitempty"`
	Truncation       string           `json:"truncation,omitempty"`
	Tools            json.RawMessage  `json:"tools,omitempty"`
	ToolChoice       json.RawMessage  `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	Reasoning        json.RawMessage  `json:"reasoning,omitempty"`
	Stream           bool             `json:"stream,omitempty"`
	StreamOptions    json.RawMessage  `json:"stream_options,omitempty"`
	ServiceTier      string           `json:"service_tier,omitempty"`
	Store            *bool            `json:"store,omitempty"`
	Metadata         json.RawMessage  `json:"metadata,omitempty"`
	User             string           `json:"user,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Include          json.RawMessage  `json:"include,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// ResponsesInput handles the polymorphic input field: string or array.
type ResponsesInput struct {
	Text  *string
	Items []ResponsesInputItem
}

func (ri ResponsesInput) MarshalJSON() ([]byte, error) {
	if ri.Text != nil && ri.Items != nil {
		return nil, fmt.Errorf("ResponsesInput: both Text and Items are set")
	}
	if ri.Text != nil {
		return json.Marshal(*ri.Text)
	}
	if ri.Items != nil {
		return json.Marshal(ri.Items)
	}
	return json.Marshal(nil)
}

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(data, &s) == nil {
		ri.Text = &s
		return nil
	}
	var items []ResponsesInputItem
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	ri.Items = items
	return nil
}

// ResponsesInputItem represents a single item in the Responses API input array.
type ResponsesInputItem struct {
	Type     string          `json:"type"` // message, function_call, function_call_output
	Role     string          `json:"role,omitempty"`
	Content  MessageContent  `json:"content,omitempty"`
	CallID   string          `json:"call_id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Output   string          `json:"output,omitempty"`
	ID       string          `json:"id,omitempty"`
	Status   string          `json:"status,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

// OpenAIResponsesResponse represents an OpenAI Responses API response.
type OpenAIResponsesResponse struct {
	ID        string               `json:"id"`
	Object    string               `json:"object"`
	CreatedAt float64              `json:"created_at"`
	Model     string               `json:"model"`
	Output    []ResponsesOutputItem `json:"output"`
	Usage     *ResponsesUsage       `json:"usage,omitempty"`
	Status    string               `json:"status,omitempty"`
	Metadata  json.RawMessage      `json:"metadata,omitempty"`
}

// ResponsesOutputItem represents a single output item.
type ResponsesOutputItem struct {
	Type      string          `json:"type"` // message, function_call
	ID        string          `json:"id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   []ContentPart   `json:"content,omitempty"`
	Status    string          `json:"status,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
}

// ResponsesUsage represents usage in a Responses API response.
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
```

- [ ] **Step 2: Run go build + commit**

```bash
go build ./internal/relay/provider/schema/...
git add internal/relay/provider/schema/openai_responses.go
git commit -m "feat(schema): add OpenAI Responses API request/response structs"
```

---

### Task 4: Create schema/anthropic.go, schema/gemini.go, schema/gemini_cli.go

**Files:**
- Create: `internal/relay/provider/schema/anthropic.go`
- Create: `internal/relay/provider/schema/gemini.go`
- Create: `internal/relay/provider/schema/gemini_cli.go`

- [ ] **Step 1: Define Anthropic Messages API structs**

```go
package schema

import "encoding/json"

// AnthropicRequest represents an Anthropic Messages API request.
type AnthropicRequest struct {
	Model         string          `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system,omitempty"` // string or []SystemBlock
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Tools         json.RawMessage `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage `json:"thinking,omitempty"` // {type,budget_tokens}
	Metadata      json.RawMessage `json:"metadata,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// AnthropicMessage represents a single message in the Anthropic Messages API.
type AnthropicMessage struct {
	Role    string               `json:"role"` // user, assistant
	Content []AnthropicContentBlock `json:"content"`
}

// AnthropicContentBlock represents a content block in an Anthropic message.
type AnthropicContentBlock struct {
	Type  string          `json:"type"` // text, image, tool_use, tool_result, thinking
	Text  string          `json:"text,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`    // for tool_use
	ID    string          `json:"id,omitempty"`        // for tool_use, thinking
	Name  string          `json:"name,omitempty"`      // for tool_use
	ToolUseID string      `json:"tool_use_id,omitempty"` // for tool_result
	ContentStr string     `json:"content,omitempty"`    // for tool_result (can be string or array)
	IsError    bool       `json:"is_error,omitempty"`   // for tool_result
	Source *AnthropicImageSource `json:"source,omitempty"` // for image
	Thinking string        `json:"thinking,omitempty"`  // for thinking block
	Signature string       `json:"signature,omitempty"` // for thinking block (thoughtSignature)

	Extra map[string]json.RawMessage `json:"-"`
}

// AnthropicImageSource represents an image source in Anthropic format.
type AnthropicImageSource struct {
	Type       string `json:"type"`        // base64, url
	MediaType  string `json:"media_type"`
	Data       string `json:"data,omitempty"`
	URL        string `json:"url,omitempty"`
}

// AnthropicResponse represents an Anthropic Messages API response.
type AnthropicResponse struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"`
	Role         string                `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                `json:"model"`
	StopReason   string                `json:"stop_reason,omitempty"`
	StopSequence string                `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage        `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}
```

- [ ] **Step 2: Define Gemini REST API structs**

```go
package schema

import "encoding/json"

// GeminiRequest represents a Gemini generateContent API request.
type GeminiRequest struct {
	Contents         []GeminiContent    `json:"contents"`
	SystemInstruction *GeminiContent    `json:"systemInstruction,omitempty"`
	Tools            json.RawMessage    `json:"tools,omitempty"`
	ToolConfig       *GeminiToolConfig  `json:"toolConfig,omitempty"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings   json.RawMessage    `json:"safetySettings,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// GeminiContent represents a content entry in Gemini format.
type GeminiContent struct {
	Role  string        `json:"role,omitempty"` // user, model
	Parts []GeminiPart  `json:"parts"`
}

// GeminiPart represents a single part in a Gemini content entry.
type GeminiPart struct {
	Text         string          `json:"text,omitempty"`
	InlineData   *GeminiBlob     `json:"inlineData,omitempty"`
	FunctionCall *GeminiFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFuncResponse `json:"functionResponse,omitempty"`
	FileData     *GeminiFileData `json:"fileData,omitempty"`
	Thought      bool            `json:"thought,omitempty"`       // for thinking parts
	ThoughtSignature string      `json:"thoughtSignature,omitempty"` // for thinking signatures
	ExecutableCode  *GeminiExecutableCode `json:"executableCode,omitempty"`
	CodeExecutionResult *GeminiCodeResult `json:"codeExecutionResult,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type GeminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type GeminiFuncResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type GeminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri,omitempty"`
}

type GeminiExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type GeminiCodeResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output"`
}

type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // AUTO, NONE, ANY, VALIDATED
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type GeminiGenerationConfig struct {
	MaxOutputTokens  *int     `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	TopK             *int     `json:"topK,omitempty"`
	CandidateCount   *int     `json:"candidateCount,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
	ThinkingConfig   json.RawMessage `json:"thinkingConfig,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// GeminiResponse represents a Gemini generateContent API response.
type GeminiResponse struct {
	Candidates     []GeminiCandidate   `json:"candidates,omitempty"`
	UsageMetadata  *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion   string              `json:"modelVersion,omitempty"`
}

type GeminiCandidate struct {
	Content       *GeminiContent `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	Index         int            `json:"index,omitempty"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}
```

- [ ] **Step 3: Define Gemini CLI (Antigravity) envelope struct**

```go
package schema

// GeminiCLIRequest represents a Gemini CLI / Antigravity API request.
// It wraps a standard GeminiRequest in an envelope with model, project,
// and other metadata required by the cloudcode-pa.googleapis.com backend.
type GeminiCLIRequest struct {
	Model      string        `json:"model"`
	Project    string        `json:"project"`
	UserAgent  string        `json:"userAgent,omitempty"`
	Request    GeminiRequest `json:"request"`
	RequestType string       `json:"requestType,omitempty"` // "image_gen", "agent"
	RequestID  string        `json:"requestId,omitempty"`
	SessionID  string        `json:"sessionId,omitempty"`
}

// GeminiCLIResponse wraps a GeminiResponse in the CLI envelope.
type GeminiCLIResponse struct {
	Response GeminiResponse `json:"response"`
}
```

- [ ] **Step 4: Run go build + commit all three**

```bash
go build ./internal/relay/provider/schema/...
git add internal/relay/provider/schema/anthropic.go internal/relay/provider/schema/gemini.go internal/relay/provider/schema/gemini_cli.go
git commit -m "feat(schema): add Anthropic, Gemini, and Gemini CLI structs"
```

---

## Phase 2: New InternalRequest + Bidirectional Conversion

### Task 5: Create convert/ package with new InternalRequest

**Files:**
- Create: `internal/relay/provider/convert/internal.go`
- Create: `internal/relay/provider/convert/registry.go`

- [ ] **Step 1: Define new InternalRequest with Instructions field**

```go
// Package convert implements the protocol conversion layer using strong-typed
// schema structs and a simplified InternalRequest as the intermediate format.
package convert

import (
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// Format identifies a protocol format for conversion dispatch.
type Format string

const (
	FormatOpenAIChatCompletions Format = "openai_chat"
	FormatOpenAIResponses       Format = "openai_responses"
	FormatAnthropic             Format = "anthropic"
	FormatGemini                Format = "gemini"
	FormatGeminiCLI             Format = "gemini_cli"
)

// InternalRequest is the protocol-neutral intermediate representation.
// System/developer messages are extracted into Instructions; Messages
// only contains user, assistant, and tool roles.
type InternalRequest struct {
	Model    string
	Stream   bool
	Messages []InternalMessage
	Tools    []schema.Tool

	// Instructions carries the unified system prompt extracted from:
	//   - OpenAI Chat: messages[role=system/developer]
	//   - OpenAI Responses: instructions field
	//   - Anthropic: system field
	//   - Gemini: systemInstruction
	// Always serialized in Responses output (including empty string).
	Instructions *string

	// Generation parameters (pointer = unset vs zero value)
	MaxTokens   *int
	Temperature *float64
	TopP        *float64
	TopK        *int
	StopWords   []string

	// Protocol-specific fields passed through as raw JSON
	Reasoning      json.RawMessage
	ToolChoice     json.RawMessage
	ResponseFormat json.RawMessage
	ParallelToolCalls *bool
	FrequencyPenalty  *float64
	PresencePenalty   *float64
	N                 *int
	Seed              *int64
	LogProbs          *bool
	TopLogProbs       *int
	LogitBias         json.RawMessage
	ServiceTier       string
	Store             *bool
	Thinking          json.RawMessage // Anthropic extended thinking config
	SafetySettings    json.RawMessage // Gemini safety settings
	CandidateCount    *int            // Gemini candidate count

	// Extra preserves protocol-specific fields for same-format passthrough.
	Extra map[string]json.RawMessage

	// SourceFormat records which protocol this was parsed from,
	// enabling selective field restoration during FromInternal.
	SourceFormat Format
}

// InternalMessage represents a single message in the intermediate format.
// Role is always "user", "assistant", or "tool" — never "system" or "developer".
type InternalMessage struct {
	Role             string
	Content          []schema.ContentPart
	ToolCalls        []schema.ToolCall
	ToolResult       *schema.ToolResult
	ReasoningContent []schema.ContentPart
	Name             string // for named messages
}

// InternalResponse is the protocol-neutral response intermediate.
type InternalResponse struct {
	ID      string
	Model   string
	Choices []InternalChoice
	Usage   schema.Usage
	Raw     json.RawMessage // preserved for same-format passthrough
}

// InternalChoice represents a single choice in a response.
type InternalChoice struct {
	Index            int
	Role             string
	Content          []schema.ContentPart
	ToolCalls        []schema.ToolCall
	FinishReason     string
	ReasoningContent []schema.ContentPart
	Refusal          string
}
```

- [ ] **Step 2: Define the conversion registry**

```go
package convert

import "fmt"

// toInternalFunc converts raw protocol bytes into an InternalRequest.
type toInternalFunc func(body []byte) (*InternalRequest, error)

// fromInternalFunc converts an InternalRequest into raw protocol bytes.
type fromInternalFunc func(ir *InternalRequest) ([]byte, error)

var toInternalRegistry = map[Format]toInternalFunc{}
var fromInternalRegistry = map[Format]fromInternalFunc{}

func RegisterToInternal(f Format, fn toInternalFunc) {
	toInternalRegistry[f] = fn
}

func RegisterFromInternal(f Format, fn fromInternalFunc) {
	fromInternalRegistry[f] = fn
}

// ConvertRequest converts a request from clientFormat to upstreamFormat.
func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	toInternal, ok := toInternalRegistry[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format %q", clientFormat)
	}
	fromInternal, ok := fromInternalRegistry[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format %q", upstreamFormat)
	}

	ir, err := toInternal(body)
	if err != nil {
		return nil, fmt.Errorf("ToInternal(%s): %w", clientFormat, err)
	}
	result, err := fromInternal(ir)
	if err != nil {
		return nil, fmt.Errorf("FromInternal(%s): %w", upstreamFormat, err)
	}
	return result, nil
}

// ToInternalOnly converts a request body to InternalRequest without converting back.
// Useful for extracting model/messages for routing decisions.
func ToInternalOnly(format Format, body []byte) (*InternalRequest, error) {
	toInternal, ok := toInternalRegistry[format]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format %q", format)
	}
	return toInternal(body)
}

// response converter types
type toResponseInternalFunc func(body []byte) (*InternalResponse, error)
type fromResponseInternalFunc func(ir *InternalResponse) ([]byte, error)

var toResponseRegistry = map[Format]toResponseInternalFunc{}
var fromResponseRegistry = map[Format]fromResponseInternalFunc{}

func RegisterToResponseInternal(f Format, fn toResponseInternalFunc) {
	toResponseRegistry[f] = fn
}

func RegisterFromResponseInternal(f Format, fn fromResponseInternalFunc) {
	fromResponseRegistry[f] = fn
}

func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	toResp, ok := toResponseRegistry[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no response ToInternal converter for format %q", upstreamFormat)
	}
	fromResp, ok := fromResponseRegistry[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no response FromInternal converter for format %q", clientFormat)
	}
	ir, err := toResp(body)
	if err != nil {
		return nil, err
	}
	return fromResp(ir)
}
```

- [ ] **Step 3: Run go build + commit**

```bash
go build ./internal/relay/provider/convert/...
git add internal/relay/provider/convert/
git commit -m "feat(convert): add new InternalRequest with Instructions field and registry"
```

---

### Task 6: Implement OpenAI Chat ToInternal / FromInternal

**Files:**
- Create: `internal/relay/provider/convert/openai_chat.go`
- Create: `internal/relay/provider/convert/openai_chat_test.go`

- [ ] **Step 1: Implement OpenAIChatRequest.ToInternal()**

Key logic:
- Parse body into `schema.OpenAIChatRequest`
- Extract `messages[role=system/developer]` → concatenate into `Instructions`
- Remaining messages → `InternalMessage` with appropriate role mapping
- Tool calls, tool results mapped to schema types
- All typed fields (temperature, max_tokens, etc.) mapped directly
- Unknown top-level keys → `Extra`

- [ ] **Step 2: Implement InternalToOpenAIChat()**

Key logic:
- `Instructions` → prepend as `role=system` ChatMessage
- `Messages` → ChatMessage with role/content/tool_calls mapping
- Tool calls → schema.ToolCall with function.name/arguments
- Tool results → ChatMessage with `role=tool`, `tool_call_id`
- All typed fields written back
- `Extra` keys merged into output

- [ ] **Step 3: Register converters in init()**

- [ ] **Step 4: Write tests for round-trip: Chat → Internal → Chat**

Test cases:
- Simple user/assistant conversation
- System message → Instructions extraction and restoration
- Tool calls (assistant with tool_calls + tool results)
- Content polymorphism (string vs array content)
- Extra fields preserved through round-trip
- **Critical: no system message → Instructions is nil, FromInternal produces no system message**

- [ ] **Step 5: Run tests + commit**

```bash
go test ./internal/relay/provider/convert/... -v -run OpenAIChat
git add internal/relay/provider/convert/openai_chat.go internal/relay/provider/convert/openai_chat_test.go
git commit -m "feat(convert): implement OpenAI Chat bidirectional conversion with Instructions"
```

---

### Task 7: Implement OpenAI Responses ToInternal / FromInternal

**Files:**
- Create: `internal/relay/provider/convert/openai_responses.go`
- Create: `internal/relay/provider/convert/openai_responses_test.go`

- [ ] **Step 1: Implement OpenAIResponsesRequest.ToInternal()**

Key logic:
- `instructions` field → `Instructions` (always set, even if empty)
- `input` items: `message` → InternalMessage, `function_call` → assistant with ToolCalls, `function_call_output` → tool with ToolResult
- Input string (bare) → single user message

- [ ] **Step 2: Implement InternalToOpenAIResponses() — THE BUG FIX**

Key logic:
- `Instructions` → `instructions` field (**ALWAYS written, even if nil → empty string**)
- `Messages` with tool_calls → separate `function_call` items
- Tool results → `function_call_output` items
- User/assistant messages → `message` type items with appropriate content part types (`input_text`/`output_text`)
- `Reasoning`, `Tools`, `ToolChoice` passed through as raw JSON

**Critical fix**: Previously `instructions` was only emitted if non-empty. Now:
```go
if ir.Instructions != nil {
    resp["instructions"] = *ir.Instructions
} else {
    resp["instructions"] = ""
}
```

- [ ] **Step 3: Write tests — especially the instructions bug fix**

Test cases:
- **No system message → instructions="" is emitted** (the bug fix)
- System message round-trip: Chat system → Internal → Responses instructions
- function_call / function_call_output mapping
- Input as bare string
- Content part type mapping (input_text vs output_text)

- [ ] **Step 4: Run tests + commit**

```bash
go test ./internal/relay/provider/convert/... -v -run OpenAIResponses
git add internal/relay/provider/convert/openai_responses.go internal/relay/provider/convert/openai_responses_test.go
git commit -m "fix(convert): implement OpenAI Responses conversion with instructions bug fix"
```

---

### Task 8: Implement Anthropic, Gemini, GeminiCLI conversions

**Files:**
- Create: `internal/relay/provider/convert/anthropic.go`
- Create: `internal/relay/provider/convert/anthropic_test.go`
- Create: `internal/relay/provider/convert/gemini.go`
- Create: `internal/relay/provider/convert/gemini_test.go`
- Create: `internal/relay/provider/convert/gemini_cli.go`

- [ ] **Step 1: Implement Anthropic ToInternal/FromInternal**

Key logic:
- `system` field → `Instructions` (string or []SystemBlock → extracted text)
- `messages` → InternalMessage; `tool_use` blocks → ToolCalls; `tool_result` blocks → ToolResult
- `thinking` config → `Thinking` raw JSON
- `tool_choice` → raw JSON (Anthropic format differs: "any"/"tool" vs "required"/"function")

- [ ] **Step 2: Implement Gemini ToInternal/FromInternal**

Key logic:
- `systemInstruction` → `Instructions`
- `contents` → InternalMessage; `functionCall` → ToolCalls; `functionResponse` → ToolResult
- `generationConfig` → typed fields (maxOutputTokens, temperature, etc.)
- `safetySettings` → raw JSON
- Tool call ID generation: use deterministic IDs based on function name + index (reference CLIProxyAPI)
- Image parts: `inlineData` with image MIME → ContentPart with Type="image_url", data URI

- [ ] **Step 3: Implement GeminiCLI ToInternal/FromInternal**

Key logic:
- `ToInternal`: unwrap envelope, delegate to Gemini
- `FromInternal`: delegate to Gemini, then wrap in envelope with model/project/userAgent/requestType/requestId/sessionId
- Claude models: set `toolConfig.functionCallingConfig.mode = "VALIDATED"`
- Non-Claude models: remove `maxOutputTokens`, remove `safetySettings`
- SessionID: SHA-256 of first user message text

- [ ] **Step 4: Write tests for each protocol**

- [ ] **Step 5: Run all tests + commit**

```bash
go test ./internal/relay/provider/convert/... -v
git add internal/relay/provider/convert/
git commit -m "feat(convert): implement Anthropic, Gemini, and GeminiCLI bidirectional conversions"
```

---

### Task 9: Implement response conversions

**Files:**
- Create: `internal/relay/provider/convert/response_openai.go`
- Create: `internal/relay/provider/convert/response_anthropic.go`
- Create: `internal/relay/provider/convert/response_gemini.go`

- [ ] **Step 1: Implement OpenAI Chat + Responses response converters**

- `openaiChatResponseToInternal`: Chat response → InternalResponse (content, tool_calls, usage)
- `internalToOpenAIChatResponse`: InternalResponse → Chat response JSON
- `openaiResponsesResponseToInternal`: Responses response → InternalResponse (output items → content/tool_calls)
- `internalToOpenAIResponsesResponse`: InternalResponse → Responses response JSON (reconstruct output items)

- [ ] **Step 2: Implement Anthropic response converters**

- `anthropicResponseToInternal`: Anthropic response → InternalResponse (content blocks → content/tool_calls, stop_reason mapping)
- `internalToAnthropicResponse`: InternalResponse → Anthropic response JSON

- [ ] **Step 3: Implement Gemini/GeminiCLI response converters**

- `geminiResponseToInternal`: Gemini response → InternalResponse (candidates → choices, functionCall → tool_calls)
- `internalToGeminiResponse`: InternalResponse → Gemini response JSON
- Handle both direct and Code Assist wrapped response formats

- [ ] **Step 4: Register all response converters + write tests**

- [ ] **Step 5: Run all tests + commit**

```bash
go test ./internal/relay/provider/convert/... -v
git add internal/relay/provider/convert/response_*.go
git commit -m "feat(convert): implement bidirectional response conversions for all protocols"
```

---

## Phase 3: Streaming State Machines

### Task 10: Create stream/ package with StreamConverter interface

**Files:**
- Create: `internal/relay/provider/stream/converter.go`
- Create: `internal/relay/provider/stream/pool.go`

- [ ] **Step 1: Define StreamConverter interface and registry**

```go
package stream

import "github.com/AutoCONFIG/uapi/internal/relay/provider/convert"

// StreamConverter converts SSE lines from one protocol format to another.
type StreamConverter interface {
	// Convert processes a single SSE data line and returns zero or more
	// SSE data lines in the target format.
	Convert(line []byte) []byte
	// Done returns any final events needed when the stream ends.
	Done() []byte
	// Reset clears all internal state for pool return.
	Reset()
}

// FormatPair identifies a conversion direction.
type FormatPair struct {
	Upstream convert.Format
	Client   convert.Format
}

var registry = map[FormatPair]func() StreamConverter{}

func Register(pair FormatPair, factory func() StreamConverter) {
	registry[pair] = factory
}

// NewConverter creates a StreamConverter for the given direction.
// Returns nil if no converter is registered (same-format passthrough).
func NewConverter(upstream, client convert.Format) StreamConverter {
	factory, ok := registry[FormatPair{Upstream: upstream, Client: client}]
	if !ok {
		return nil
	}
	return factory()
}
```

- [ ] **Step 2: Define sync.Pool helpers**

```go
package stream

import "sync"

// Pool is a generic sync.Pool wrapper for StreamConverter states.
type Pool struct {
	pool sync.Pool
}

// NewPool creates a pool with the given factory function.
func NewPool(factory func() StreamConverter) *Pool {
	return &Pool{
		pool: sync.Pool{New: func() interface{} { return factory() }},
	}
}

func (p *Pool) Get() StreamConverter {
	return p.pool.Get().(StreamConverter)
}

func (p *Pool) Put(c StreamConverter) {
	c.Reset()
	p.pool.Put(c)
}
```

- [ ] **Step 3: Run go build + commit**

```bash
go build ./internal/relay/provider/stream/...
git add internal/relay/provider/stream/
git commit -m "feat(stream): add StreamConverter interface, registry, and sync.Pool helpers"
```

---

### Task 11: Implement Responses → Chat SSE stream converter

**Files:**
- Create: `internal/relay/provider/stream/responses_to_chat.go`
- Create: `internal/relay/provider/stream/responses_to_chat_test.go`

- [ ] **Step 1: Implement responsesToChatConverter**

State machine tracks:
- model, ID, createdAt from `response.created`
- Current output item type (message vs function_call)
- Text buffer for `output_text.delta` → Chat `delta.content`
- Tool call tracking: `callID → {index, name, args}` for `function_call_arguments.delta` → Chat `delta.tool_calls`
- Finish reason from `response.completed` → Chat `finish_reason`
- Usage from `response.completed` → Chat `usage`

Reference new-api's canonical ID mapping pattern for `item.id → call_id` resolution.

- [ ] **Step 2: Write tests with mock SSE events**

Test cases:
- Simple text stream: `response.created` → `output_text.delta` × N → `response.completed`
- Tool call stream: `output_item.added(function_call)` → `function_call_arguments.delta` × N → `output_item.done` → `response.completed`
- Mixed text + tool calls
- Token fallback when `response.completed` has no usage
- Empty stream (error case)

- [ ] **Step 3: Register in init() + commit**

```bash
go test ./internal/relay/provider/stream/... -v -run ResponsesToChat
git add internal/relay/provider/stream/responses_to_chat.go internal/relay/provider/stream/responses_to_chat_test.go
git commit -m "feat(stream): implement Responses → Chat SSE converter with sync.Pool"
```

---

### Task 12: Implement remaining stream converters

**Files:**
- Create: `internal/relay/provider/stream/chat_to_responses.go`
- Create: `internal/relay/provider/stream/anthropic_to_chat.go`
- Create: `internal/relay/provider/stream/chat_to_anthropic.go`
- Create: `internal/relay/provider/stream/gemini_to_chat.go`
- Create: `internal/relay/provider/stream/chat_to_gemini.go`

- [ ] **Step 1: Implement Chat → Responses SSE converter**

Inverse of Task 11: Chat SSE `delta.content` → `output_text.delta`, `delta.tool_calls` → `function_call_arguments.delta`.

- [ ] **Step 2: Implement Anthropic ↔ Chat SSE converters**

Migrate logic from current `anthropic/streaming.go`:
- `anthropicToChat`: `message_start` → role, `content_block_delta(text_delta)` → content, `content_block_delta(input_json_delta)` → tool_calls, `message_delta` → finish_reason + usage
- `chatToAnthropic`: Inverse conversion

- [ ] **Step 3: Implement Gemini ↔ Chat SSE converters**

Migrate logic from current `gemini/streaming.go`:
- `geminiToChat`: Handle wrapped format, `candidates[0].content.parts` → content/tool_calls, `thought:true` → reasoning_content
- `chatToGemini`: Inverse conversion

- [ ] **Step 4: Register all converters + run tests + commit**

```bash
go test ./internal/relay/provider/stream/... -v
git add internal/relay/provider/stream/
git commit -m "feat(stream): implement all bidirectional SSE stream converters"
```

---

## Phase 4: Wire Into Existing Infrastructure

### Task 13: Update provider/types.go — Add FormatGeminiCLI, simplify InternalRequest

**Files:**
- Modify: `internal/relay/provider/types.go`

- [ ] **Step 1: Add FormatGeminiCLI constant**

```go
FormatGeminiCLI Format = "gemini_cli"  // Gemini CLI / Antigravity protocol
```

- [ ] **Step 2: Add Instructions field to existing InternalRequest (backward compatible)**

Add `Instructions *string` field. Existing code continues to work since it's a new field. Old conversion code ignores it; new conversion code in convert/ uses it.

- [ ] **Step 3: Run full build to verify no breakage**

```bash
go build ./...
git add internal/relay/provider/types.go
git commit -m "feat(provider): add FormatGeminiCLI and Instructions to InternalRequest"
```

---

### Task 14: Update each Adaptor to delegate to new convert/ package

**Files:**
- Modify: `internal/relay/provider/openai/adaptor.go`
- Modify: `internal/relay/provider/anthropic/adaptor.go`
- Modify: `internal/relay/provider/gemini/adaptor.go`
- Modify: `internal/relay/provider/antigravity/adaptor.go`

- [ ] **Step 1: Update OpenAI Adaptor**

Change `ToInternal` to call `convert.ToInternalOnly(convert.FormatOpenAIChatCompletions, body)` or `convert.FormatOpenAIResponses` based on the channel's APIFormat.
Change `FromInternal` to call `convert.ConvertRequest` or direct `fromInternalRegistry` lookup.

- [ ] **Step 2: Update Anthropic Adaptor**

Same pattern: delegate to `convert.ToInternalOnly(convert.FormatAnthropic, body)` and `convert` registry.

- [ ] **Step 3: Update Gemini Adaptor**

Delegate to `convert.ToInternalOnly(convert.FormatGemini, body)`.

- [ ] **Step 4: Update Antigravity Adaptor**

Change from `gemini.RequestToInternal` to `convert.ToInternalOnly(convert.FormatGeminiCLI, body)`.
Change `FromInternal` to use `convert` registry with `FormatGeminiCLI`.

- [ ] **Step 5: Update streaming: use stream/ package**

Each adaptor's `CreateReverseStreamConverter` delegates to `stream.NewConverter(upstream, client)`.

- [ ] **Step 6: Run full build + existing tests**

```bash
go build ./...
go test ./internal/relay/... -v
git commit -am "refactor(adaptors): delegate conversion to new convert/ and stream/ packages"
```

---

### Task 15: Implement same-format passthrough in handler.go

**Files:**
- Modify: `internal/relay/handler.go`

- [ ] **Step 1: Add same-format JSON passthrough path**

When `clientFormat == upstreamFormat`, skip `ConvertRequestWithAdaptor`. Instead, do surgical JSON edits:
- Replace `model` field using gjson/sjson
- Inject `stream: true` if force stream
- Inject `stream_options.include_usage: true` if needed
- All other fields pass through untouched

- [ ] **Step 2: Same-format response passthrough**

When `clientFormat == upstreamFormat`, return upstream response bytes directly without `ConvertResponse`.

- [ ] **Step 3: Run full build + tests**

```bash
go build ./...
go test ./internal/relay/... -v
git commit -am "feat(handler): same-format passthrough bypasses Internal round-trip"
```

---

## Phase 5: Cleanup

### Task 16: Remove old conversion code

**Files:**
- Remove: `internal/relay/provider/anthropic/to_internal.go`
- Remove: `internal/relay/provider/anthropic/from_internal.go`
- Remove: `internal/relay/provider/anthropic/response_convert.go`
- Remove: `internal/relay/provider/anthropic/streaming.go`
- Remove: `internal/relay/provider/gemini/to_internal.go`
- Remove: `internal/relay/provider/gemini/from_internal.go`
- Remove: `internal/relay/provider/gemini/response_convert.go`
- Remove: `internal/relay/provider/gemini/streaming.go`
- Remove: `internal/relay/provider/openai/to_internal.go`
- Remove: `internal/relay/provider/openai/response_convert.go`
- Remove: `internal/relay/provider/openai/streaming.go`
- Remove: `internal/relay/provider/openai/responses.go`
- Remove: `internal/relay/provider/convert.go` (old registry)

- [ ] **Step 1: Verify all references to old files are gone**

Search for imports of removed files and update.

- [ ] **Step 2: Run full build + all tests**

```bash
go build ./...
go test ./... -v
git commit -am "cleanup: remove old map-based conversion code, now replaced by schema/convert/stream"
```

---

### Task 17: Final integration test

**Files:**
- Create: `internal/relay/provider/convert/integration_test.go`

- [ ] **Step 1: Write cross-protocol round-trip tests**

Test all 20 conversion pairs (5 × 4 cross-format directions):
- Chat ↔ Responses (critical: instructions bug fix)
- Chat ↔ Anthropic
- Chat ↔ Gemini
- Chat ↔ GeminiCLI
- Responses ↔ Anthropic
- Responses ↔ Gemini
- etc.

- [ ] **Step 2: Write streaming integration tests**

Test key streaming directions with realistic SSE event sequences.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -v -count=1
git commit -am "test(convert): add cross-protocol integration tests"
```

---

## Self-Review Checklist

1. **Spec coverage**: Each section of the spec maps to tasks:
   - Section 1 (Schema Structs) → Tasks 1-4
   - Section 2 (InternalRequest + Bidirectional) → Tasks 5-9
   - Section 3 (Streaming State Machines) → Tasks 10-12
   - Section 4 (Adaptor + Dispatch) → Tasks 13-15
   - Section 5 (Error Handling) → Covered in each converter
   - Section 6 (Bug Fix Checklist) → Task 7 (instructions), Task 15 (passthrough), Task 9 (response conversion)

2. **Placeholder scan**: No TBDs, TODOs, or "implement later" found.

3. **Type consistency**: `convert.Format` used consistently across all packages. `schema.MessageContent` used for all content fields. `convert.InternalRequest.Instructions` is `*string` everywhere.
