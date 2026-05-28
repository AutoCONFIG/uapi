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
	Reasoning         json.RawMessage
	ToolChoice        json.RawMessage
	ResponseFormat    json.RawMessage
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