package provider

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

type Format string

const (
	FormatOpenAIChatCompletions Format = "openai_chat"
	FormatOpenAIResponses       Format = "openai_responses"
	FormatAnthropic             Format = "anthropic"
	FormatGemini                Format = "gemini"
	FormatGeminiCode            Format = "gemini_code"
	FormatAntigravity           Format = "antigravity"
)

type InternalRequest struct {
	Model       string
	Messages    []InternalMessage
	Tools       []InternalTool
	ToolChoice  *InternalToolChoice
	Stream      bool
	MaxTokens   *int
	Temperature *float64
	TopP        *float64
	TopK        *int // Anthropic/Gemini top_k
	StopWords   []string
	Metadata    map[string]interface{}

	// Common generation parameters preserved across protocol conversion.
	FrequencyPenalty  *float64
	PresencePenalty   *float64
	N                 *int
	Seed              *int64
	LogProbs          bool
	TopLogProbs       *int
	ResponseFormat    interface{} // json_object, json_schema, etc.
	LogitBias         interface{}
	ParallelToolCalls *bool
	ServiceTier       string // auto, default
	Store             *bool  // OpenAI Responses store

	// Provider-specific reasoning/thinking configuration.
	Reasoning interface{} // OpenAI Responses reasoning effort; Anthropic thinking budget
	Thinking  interface{} // Anthropic extended thinking config {type, budget_tokens}

	// Gemini-specific fields.
	SafetySettings interface{}
	CandidateCount *int
	Provider       string // Gemini provider field

	// ExtraParams captures fields not explicitly modeled above. During
	// same-protocol passthrough these are merged back into the output
	// for lossless round-tripping. During cross-protocol conversion,
	// unmapped ExtraParams fields are silently dropped with a warning log.
	ExtraParams map[string]interface{}
}

type InternalMessage struct {
	Role             string
	Content          []InternalContentPart
	ToolCalls        []InternalToolCall
	ToolResult       *InternalToolResult
	ReasoningContent []InternalContentPart // Extended thinking / reasoning content
}

type InternalContentPart struct {
	Type        string // text, image_url, refusal, reasoning, thinking
	Text        string
	ImageURL    *string
	ImageDetail string
	Refusal     string                 // OpenAI Chat Completions API refusal content
	Extra       map[string]interface{} // Unknown keys (e.g. cache_control) for lossless round-tripping
}

type InternalToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type InternalToolResult struct {
	ToolCallID string
	Name       string
	Content    string
	IsError    bool
}

type InternalTool struct {
	Type        string
	Name        string
	Description string
	Parameters  interface{}
}

type InternalToolChoice struct {
	Type     string // auto, none, required, function
	Function string
}

type InternalResponse struct {
	ID       string
	Model    string
	Choices  []InternalChoice
	Usage    InternalUsage
	Metadata map[string]interface{}
}

type InternalChoice struct {
	Index        int
	Message      InternalMessage
	FinishReason string
}

type InternalUsage struct {
	PromptTokens             int
	CompletionTokens         int
	CacheCreationInputTokens int // Anthropic cache_creation_input_tokens
	CacheReadInputTokens     int // Anthropic cache_read_input_tokens / OpenAI cached_tokens
	PromptTokensDetails      map[string]interface{}
	CompletionTokensDetails  map[string]interface{}
}

// ToInt converts an interface{} (float64, int, etc.) to int.
func ToInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	default:
		return 0
	}
	return 0
}

// DecodeJSONUseNumber decodes JSON into interface maps without coercing numbers
// to float64, preserving tool-call argument precision across protocol conversion.
func DecodeJSONUseNumber(body []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	return dec.Decode(v)
}

func ToFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// RandomHex generates a random hex string of n bytes using crypto/rand.
// Falls back to a timestamp-based hex if crypto/rand fails.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		ts := fmt.Sprintf("%x", time.Now().UnixNano())
		for len(ts) < n*2 {
			ts += ts
		}
		return ts[:n*2]
	}
	return hex.EncodeToString(b)
}

type Adaptor interface {
	Init(channel *db.Channel, account *db.Account)
	SetRequestParams(model string, stream bool)
	GetRequestURL(path string) (string, error)
	SetupRequestHeader(req *fasthttp.Request, credentials string) error
	ToInternal(body []byte) (*InternalRequest, error)
	FromInternal(req *InternalRequest) ([]byte, error)
	ConvertStreamLine(line []byte) []byte
	ConvertSSEBuffer(sseBody []byte) []byte
	CreateReverseStreamConverter() func([]byte) []byte
	ParseUsage(respBody []byte) (promptTokens, completionTokens int, err error)
	ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
	// ParseUsageFull returns full usage including cache tokens
	ParseUsageFull(respBody []byte) (InternalUsage, error)
	GetChannelType() string
}
