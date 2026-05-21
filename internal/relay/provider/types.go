package provider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

type Format string

const (
	FormatOpenAIChat Format = "openai_chat"
	FormatOpenAIResp Format = "openai_responses"
	FormatAnthropic  Format = "anthropic"
	FormatGemini     Format = "gemini"
	FormatGeminiCode Format = "gemini_code"
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
	StopWords   []string
	Metadata    map[string]interface{}
}

type InternalMessage struct {
	Role       string
	Content    []InternalContentPart
	ToolCalls  []InternalToolCall
	ToolResult *InternalToolResult
}

type InternalContentPart struct {
	Type     string // text, image_url
	Text     string
	ImageURL *string
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
	ID      string
	Model   string
	Choices []InternalChoice
	Usage   InternalUsage
}

type InternalChoice struct {
	Index        int
	Message      InternalMessage
	FinishReason string
}

type InternalUsage struct {
	PromptTokens     int
	CompletionTokens int
}

// ToInt converts an interface{} (float64, int, etc.) to int.
func ToInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
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
	GetChannelType() string
}
