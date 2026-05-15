package provider

import (
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

type Format string

const (
	FormatOpenAIChat Format = "openai_chat"
	FormatOpenAIResp Format = "openai_responses"
	FormatAnthropic  Format = "anthropic"
	FormatGemini     Format = "gemini"
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

type Adaptor interface {
	Init(channel *db.Channel, account *db.Account)
	GetRequestURL(path string) (string, error)
	SetupRequestHeader(req *fasthttp.Request, credentials string) error
	ToInternal(body []byte) (*InternalRequest, error)
	FromInternal(req *InternalRequest) ([]byte, error)
	ConvertStreamLine(line []byte) []byte
	ParseUsage(respBody []byte) (promptTokens, completionTokens int, err error)
	ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
	GetChannelType() string
}
