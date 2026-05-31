package convert

import (
	"bytes"
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// Format identifies a protocol format for conversion dispatch.
type Format string

const (
	FormatOpenAIChatCompletions Format = "openai_chat"
	FormatOpenAIResponses       Format = "openai_responses"
	FormatCodexResponses        Format = "codex"
	FormatAnthropic             Format = "anthropic"
	FormatClaudeCode            Format = "claude_code"
	FormatGemini                Format = "gemini"
	FormatGeminiCode            Format = "gemini_code"
	FormatGeminiCLI             Format = "gemini_cli"
	FormatAntigravity           Format = "antigravity"
)

// adapterRequest is the package-private protocol adapter view used by concrete
// serializers. Request routing is anchored on ir.Request; protocol entry points
// register IR parsers and emitters directly.
type adapterRequest struct {
	Model    string
	Stream   bool
	Messages []adapterTurn
	Tools    []schema.Tool
	// RawRequestBody preserves the exact client payload for same-protocol
	// replay/audit and for the new IR native envelope.
	RawRequestBody json.RawMessage

	// IR is the protocol-neutral representation used by request conversion.
	IR *ir.Request `json:"-"`

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
	// enabling selective field restoration during request emission.
	SourceFormat Format

	// Losses records known conversion/audit loss.
	Losses []ir.Loss `json:"-"`
}

// adapterTurn is the provider-adapter view over an ordered turn. Parts is
// the canonical ordering source.
type adapterTurn struct {
	Role    string
	Parts   []adapterItem
	Name    string // for named messages
	ItemID  string
	Status  string
	Phase   string
	RawItem json.RawMessage // original Responses input item for same-format replay
	Extra   map[string]json.RawMessage
}

// adapterItem preserves the original ordered content stream for
// provider parsers and emitters.
type adapterItem struct {
	Kind       string
	Content    schema.ContentPart
	ToolCall   schema.ToolCall
	ToolResult schema.ToolResult
	Raw        json.RawMessage
}

// adapterResponse is the internal response serialization view.
type adapterResponse struct {
	ID      string
	Model   string
	Choices []adapterChoice
	Usage   schema.Usage
	Raw     json.RawMessage // preserved for same-format passthrough
	IR      *ir.Response    `json:"-"`
	Losses  []ir.Loss       `json:"-"`
}

// adapterChoice represents a single choice in a response.
type adapterChoice struct {
	Index        int
	Role         string
	Items        []adapterItem
	FinishReason string
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
