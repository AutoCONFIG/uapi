package convert

import (
	"bytes"
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
	// InstructionsRaw preserves native system/instructions blocks when the
	// source protocol carries more than plain text.
	InstructionsRaw json.RawMessage

	// Generation parameters (pointer = unset vs zero value)
	MaxTokens      *int
	MaxTokensField string
	Temperature    *float64
	TopP           *float64
	TopK           *int
	StopWords      []string

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

	// GeminiGenerationConfigExtra preserves generationConfig keys UAPI does not
	// model yet, such as responseLogprobs, routingConfig, and media settings.
	GeminiGenerationConfigExtra map[string]json.RawMessage

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
	Parts            []InternalContentItem
	Content          []schema.ContentPart
	ToolCalls        []schema.ToolCall
	ToolResult       *schema.ToolResult
	ReasoningContent []schema.ContentPart
	Name             string // for named messages
	ItemID           string
	Status           string
	Phase            string
	RawItem          json.RawMessage // original Responses input item for same-format replay
	Extra            map[string]json.RawMessage
}

// InternalContentItem preserves the original ordered content stream for
// protocols whose message bodies are block sequences (Gemini parts,
// Anthropic content blocks, Responses input/output items). The legacy Content,
// ToolCalls, ToolResult, and ReasoningContent fields remain as indexed views for
// converters that do not need exact source ordering.
type InternalContentItem struct {
	Kind       string
	Content    schema.ContentPart
	ToolCall   schema.ToolCall
	ToolResult schema.ToolResult
	Raw        json.RawMessage
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

func decodeJSONUseNumber(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

func copyRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
