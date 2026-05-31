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

func parseOpenAIChatRequestIR(body []byte) (*ir.Request, error) {
	draft, err := parseOpenAIChatRequest(body)
	if err != nil {
		return nil, err
	}
	draft.attachRawRequest(body)
	return draft.ToIR(), nil
}

func emitOpenAIChatRequestIR(req *ir.Request) ([]byte, error) {
	return emitOpenAIChatRequest(requestDraftForTarget(req, FormatOpenAIChatCompletions))
}

func parseOpenAIResponsesRequestIR(body []byte) (*ir.Request, error) {
	draft, err := parseOpenAIResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	draft.attachRawRequest(body)
	return draft.ToIR(), nil
}

func emitOpenAIResponsesRequestIR(req *ir.Request) ([]byte, error) {
	return emitOpenAIResponsesRequest(requestDraftForTarget(req, FormatOpenAIResponses))
}

func parseAnthropicRequestIR(body []byte) (*ir.Request, error) {
	draft, err := parseAnthropicRequest(body)
	if err != nil {
		return nil, err
	}
	draft.attachRawRequest(body)
	return draft.ToIR(), nil
}

func emitAnthropicRequestIR(req *ir.Request) ([]byte, error) {
	return emitAnthropicRequest(requestDraftForTarget(req, FormatAnthropic))
}

func parseGeminiRequestIR(body []byte) (*ir.Request, error) {
	draft, err := parseGeminiRequest(body)
	if err != nil {
		return nil, err
	}
	draft.attachRawRequest(body)
	return draft.ToIR(), nil
}

func emitGeminiRequestIR(req *ir.Request) ([]byte, error) {
	return emitGeminiRequest(requestDraftForTarget(req, FormatGemini))
}

func parseGeminiCLIRequestIR(body []byte) (*ir.Request, error) {
	draft, err := parseGeminiCLIRequest(body)
	if err != nil {
		return nil, err
	}
	draft.attachRawRequest(body)
	return draft.ToIR(), nil
}

func emitGeminiCLIRequestIR(req *ir.Request) ([]byte, error) {
	return emitGeminiCLIRequest(requestDraftForTarget(req, FormatGeminiCLI))
}

func parseOpenAIChatResponseIR(body []byte) (*ir.Response, error) {
	resp, err := parseOpenAIChatResponse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(FormatOpenAIChatCompletions), nil
}

func emitOpenAIChatResponseIR(resp *ir.Response) ([]byte, error) {
	return emitOpenAIChatResponse(responseDraftFromIR(resp))
}

func parseOpenAIResponsesResponseIR(body []byte) (*ir.Response, error) {
	resp, err := parseOpenAIResponsesResponse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(FormatOpenAIResponses), nil
}

func emitOpenAIResponsesResponseIR(resp *ir.Response) ([]byte, error) {
	return emitOpenAIResponsesResponse(responseDraftFromIR(resp))
}

func parseAnthropicResponseIR(body []byte) (*ir.Response, error) {
	resp, err := parseAnthropicResponse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(FormatAnthropic), nil
}

func emitAnthropicResponseIR(resp *ir.Response) ([]byte, error) {
	return emitAnthropicResponse(responseDraftFromIR(resp))
}

func parseGeminiResponseIR(body []byte) (*ir.Response, error) {
	resp, err := parseGeminiResponse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(FormatGemini), nil
}

func emitGeminiResponseIR(resp *ir.Response) ([]byte, error) {
	return emitGeminiResponse(responseDraftFromIR(resp))
}

func parseGeminiCLIResponseIR(body []byte) (*ir.Response, error) {
	resp, err := parseGeminiCLIResponse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(FormatGeminiCLI), nil
}

func emitGeminiCLIResponseIR(resp *ir.Response) ([]byte, error) {
	return emitGeminiCLIResponse(responseDraftFromIR(resp))
}

// requestDraft is a package-private construction draft. It is not registered as
// a converter fact source; registry entry points parse into ir.Request and emit
// from ir.Request.
type requestDraft struct {
	Model    string
	Stream   bool
	Messages []requestTurnDraft
	Tools    []schema.Tool
	// RawRequestBody preserves the exact client payload for same-protocol
	// replay/audit and for the new IR native envelope.
	RawRequestBody json.RawMessage

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
	User              string
	Thinking          json.RawMessage // Anthropic extended thinking config
	SafetySettings    json.RawMessage // Gemini safety settings
	CandidateCount    *int            // Gemini candidate count

	// GeminiGenerationConfigExtra preserves generationConfig keys UAPI does not
	// model yet, such as responseLogprobs, routingConfig, and media settings.
	GeminiGenerationConfigExtra map[string]json.RawMessage

	// Extra preserves protocol-specific fields through the native envelope.
	Extra map[string]json.RawMessage

	// SourceFormat records which protocol this was parsed from,
	// enabling selective field restoration during request emission.
	SourceFormat Format

	// Losses records known conversion/audit loss.
	Losses []ir.Loss `json:"-"`
}

func (r *requestDraft) attachRawRequest(body []byte) {
	if r == nil || len(r.RawRequestBody) > 0 {
		return
	}
	r.RawRequestBody = append([]byte(nil), body...)
}

func requestDraftForTarget(req *ir.Request, target Format) *requestDraft {
	draft := requestDraftFromIR(req)
	if draft.SourceFormat == "" {
		draft.SourceFormat = protocolFormat(req.SourceProtocol)
	}
	if draft.SourceFormat == "" {
		draft.SourceFormat = target
	}
	return draft
}

// requestTurnDraft is a construction draft over one ordered IR turn. Parts
// mirrors ir.Turn.Items while protocol emitters are projected from IR.
type requestTurnDraft struct {
	Role    string
	Parts   []requestItemDraft
	Name    string // for named messages
	ItemID  string
	Status  string
	Phase   string
	RawItem json.RawMessage // original Responses input item for same-format replay
	Extra   map[string]json.RawMessage
}

// requestItemDraft preserves one ordered content/tool element while projecting
// between schema-specific structs and ir.Item.
type requestItemDraft struct {
	Kind       string
	Content    schema.ContentPart
	ToolCall   schema.ToolCall
	ToolResult schema.ToolResult
	Raw        json.RawMessage
}

// responseDraft is a package-private construction draft projected to/from
// ir.Response.
type responseDraft struct {
	ID      string
	Model   string
	Choices []responseChoiceDraft
	Usage   schema.Usage
	Raw     json.RawMessage // preserved for native replay and field recovery
	Losses  []ir.Loss       `json:"-"`
}

// responseChoiceDraft represents a protocol response choice in a response.
type responseChoiceDraft struct {
	Index        int
	Role         string
	Items        []requestItemDraft
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
