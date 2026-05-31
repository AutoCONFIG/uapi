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

// protocolRequestViewParser is a protocol-local parser stage used only by serializer-view wrappers.
type protocolRequestViewParser func(body []byte) (*protocolRequestView, error)

// protocolRequestViewEmitter is a protocol-local emitter stage used only by serializer-view wrappers.
type protocolRequestViewEmitter func(ir *protocolRequestView) ([]byte, error)

type responseParser func(body []byte) (*protocolResponseView, error)
type responseEmitter func(ir *protocolResponseView) ([]byte, error)

func parseOpenAIChatRequestIR(body []byte) (*ir.Request, error) {
	return parseRequestWithProtocolView(FormatOpenAIChatCompletions, body, parseOpenAIChatRequest)
}

func emitOpenAIChatRequestIR(req *ir.Request) ([]byte, error) {
	return emitRequestWithProtocolView(FormatOpenAIChatCompletions, req, emitOpenAIChatRequest)
}

func parseOpenAIResponsesRequestIR(body []byte) (*ir.Request, error) {
	return parseRequestWithProtocolView(FormatOpenAIResponses, body, parseOpenAIResponsesRequest)
}

func emitOpenAIResponsesRequestIR(req *ir.Request) ([]byte, error) {
	return emitRequestWithProtocolView(FormatOpenAIResponses, req, emitOpenAIResponsesRequest)
}

func parseAnthropicRequestIR(body []byte) (*ir.Request, error) {
	return parseRequestWithProtocolView(FormatAnthropic, body, parseAnthropicRequest)
}

func emitAnthropicRequestIR(req *ir.Request) ([]byte, error) {
	return emitRequestWithProtocolView(FormatAnthropic, req, emitAnthropicRequest)
}

func parseGeminiRequestIR(body []byte) (*ir.Request, error) {
	return parseRequestWithProtocolView(FormatGemini, body, parseGeminiRequest)
}

func emitGeminiRequestIR(req *ir.Request) ([]byte, error) {
	return emitRequestWithProtocolView(FormatGemini, req, emitGeminiRequest)
}

func parseGeminiCLIRequestIR(body []byte) (*ir.Request, error) {
	return parseRequestWithProtocolView(FormatGeminiCLI, body, parseGeminiCLIRequest)
}

func emitGeminiCLIRequestIR(req *ir.Request) ([]byte, error) {
	return emitRequestWithProtocolView(FormatGeminiCLI, req, emitGeminiCLIRequest)
}

func parseRequestWithProtocolView(format Format, body []byte, parse protocolRequestViewParser) (*ir.Request, error) {
	parsed, err := parseProtocolRequestView(format, body, parse)
	if err != nil {
		return nil, err
	}
	return parsed.ToIR(), nil
}

func emitRequestWithProtocolView(format Format, req *ir.Request, emit protocolRequestViewEmitter) ([]byte, error) {
	view := protocolRequestViewFromIR(req)
	if view.SourceFormat == "" {
		view.SourceFormat = protocolFormat(req.SourceProtocol)
	}
	if view.SourceFormat == "" {
		view.SourceFormat = format
	}
	return emit(view)
}

func parseProtocolRequestView(format Format, body []byte, parse protocolRequestViewParser) (*protocolRequestView, error) {
	ir, err := parse(body)
	if err != nil {
		return nil, err
	}
	attachRawRequest(ir, body)
	return ir, nil
}

func attachRawRequest(ir *protocolRequestView, body []byte) {
	if ir == nil || len(ir.RawRequestBody) > 0 {
		return
	}
	ir.RawRequestBody = append([]byte(nil), body...)
}

func parseOpenAIChatResponseIR(body []byte) (*ir.Response, error) {
	return parseResponseWithProtocolView(FormatOpenAIChatCompletions, body, parseOpenAIChatResponse)
}

func emitOpenAIChatResponseIR(resp *ir.Response) ([]byte, error) {
	return emitResponseWithProtocolView(resp, emitOpenAIChatResponse)
}

func parseOpenAIResponsesResponseIR(body []byte) (*ir.Response, error) {
	return parseResponseWithProtocolView(FormatOpenAIResponses, body, parseOpenAIResponsesResponse)
}

func emitOpenAIResponsesResponseIR(resp *ir.Response) ([]byte, error) {
	return emitResponseWithProtocolView(resp, emitOpenAIResponsesResponse)
}

func parseAnthropicResponseIR(body []byte) (*ir.Response, error) {
	return parseResponseWithProtocolView(FormatAnthropic, body, parseAnthropicResponse)
}

func emitAnthropicResponseIR(resp *ir.Response) ([]byte, error) {
	return emitResponseWithProtocolView(resp, emitAnthropicResponse)
}

func parseGeminiResponseIR(body []byte) (*ir.Response, error) {
	return parseResponseWithProtocolView(FormatGemini, body, parseGeminiResponse)
}

func emitGeminiResponseIR(resp *ir.Response) ([]byte, error) {
	return emitResponseWithProtocolView(resp, emitGeminiResponse)
}

func parseGeminiCLIResponseIR(body []byte) (*ir.Response, error) {
	return parseResponseWithProtocolView(FormatGeminiCLI, body, parseGeminiCLIResponse)
}

func emitGeminiCLIResponseIR(resp *ir.Response) ([]byte, error) {
	return emitResponseWithProtocolView(resp, emitGeminiCLIResponse)
}

func parseResponseWithProtocolView(format Format, body []byte, parse responseParser) (*ir.Response, error) {
	resp, err := parse(body)
	if err != nil {
		return nil, err
	}
	return resp.ToIR(format), nil
}

func emitResponseWithProtocolView(resp *ir.Response, emit responseEmitter) ([]byte, error) {
	return emit(protocolResponseViewFromIR(resp))
}

// protocolRequestView is the package-private protocol serializer view used by concrete
// serializers. Request routing is anchored on ir.Request; protocol entry points
// register IR parsers and emitters directly.
type protocolRequestView struct {
	Model    string
	Stream   bool
	Messages []protocolTurnView
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

// protocolTurnView is the protocol serializer view over an ordered turn. Parts is
// the canonical ordering source.
type protocolTurnView struct {
	Role    string
	Parts   []protocolItemView
	Name    string // for named messages
	ItemID  string
	Status  string
	Phase   string
	RawItem json.RawMessage // original Responses input item for same-format replay
	Extra   map[string]json.RawMessage
}

// protocolItemView preserves the original ordered content stream for
// provider parsers and emitters.
type protocolItemView struct {
	Kind       string
	Content    schema.ContentPart
	ToolCall   schema.ToolCall
	ToolResult schema.ToolResult
	Raw        json.RawMessage
}

// protocolResponseView is the internal response serialization view.
type protocolResponseView struct {
	ID      string
	Model   string
	Choices []protocolChoiceView
	Usage   schema.Usage
	Raw     json.RawMessage // preserved for native replay and field recovery
	Losses  []ir.Loss       `json:"-"`
}

// protocolChoiceView represents a protocol response choice in a response.
type protocolChoiceView struct {
	Index        int
	Role         string
	Items        []protocolItemView
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
