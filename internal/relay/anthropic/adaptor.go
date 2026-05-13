package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

type AnthropicAdaptor struct {
	channel *db.Channel
	account *db.Account
}

func (a *AnthropicAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *AnthropicAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(a.channel.Endpoint, "/")
	// Map /v1/chat/completions → /v1/messages
	if strings.HasSuffix(path, "/chat/completions") {
		return base + "/v1/messages", nil
	}
	return base + path, nil
}

func (a *AnthropicAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("x-api-key", credentials)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// openAIRequest for parsing incoming requests
type openAIRequest struct {
	Model     string        `json:"model"`
	Messages  []interface{} `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}

// anthropicRequest for sending to Anthropic API
type anthropicRequest struct {
	Model     string        `json:"model"`
	Messages  []interface{} `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream,omitempty"`
}

func (a *AnthropicAdaptor) ConvertRequest(body []byte) ([]byte, error) {
	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	anthropicReq := anthropicRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		MaxTokens: maxTokens,
		Stream:    req.Stream,
	}
	return json.Marshal(anthropicReq)
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Usage anthropicUsage `json:"usage"`
}

func (a *AnthropicAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse anthropic response: %w", err)
	}
	return resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
}

func (a *AnthropicAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(lastChunk, &resp); err != nil {
		return 0, 0, nil
	}
	return resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
}

func (a *AnthropicAdaptor) GetChannelType() string { return "anthropic" }
