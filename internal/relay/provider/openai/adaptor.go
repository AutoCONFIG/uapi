package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

type OpenAIAdaptor struct {
	channel *db.Channel
	account *db.Account
}

func (a *OpenAIAdaptor) SetRequestParams(model string, stream bool) {
	// No-op: OpenAI does not need model/stream in URL
}

func (a *OpenAIAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *OpenAIAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(a.channel.Endpoint, "/")
	if strings.HasPrefix(path, "/v1/images/") {
		return base + path, nil
	}
	if a.channel.APIFormat == "responses" || a.channel.APIFormat == "codex" {
		// Map /v1/chat/completions → /v1/responses
		if strings.HasSuffix(path, "/chat/completions") {
			return base + "/v1/responses", nil
		}
	}
	return base + path, nil
}

func (a *OpenAIAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Authorization", "Bearer "+provider.ExtractCredentialKey(credentials))
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// --- Intermediate format conversion ---

func (a *OpenAIAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	return openaiChatToInternal(body)
}

func (a *OpenAIAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	if a.channel.APIFormat == "responses" || a.channel.APIFormat == "codex" {
		// Convert InternalRequest to OpenAI Responses API format
		return internalToResponses(req)
	}
	return internalToOpenAIChat(req)
}

// --- Response/stream handling ---

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIResponse struct {
	Usage openAIUsage `json:"usage"`
}

func (a *OpenAIAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp openAIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse openai response: %w", err)
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
}

func (a *OpenAIAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	// OpenAI streams send usage in the last chunk's [DONE] preceder
	// Format: data: {"choices":[],"usage":{...}}
	var resp openAIResponse
	if err := json.Unmarshal(lastChunk, &resp); err != nil {
		return 0, 0, nil // might not have usage in stream
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
}

// ConvertStreamLine passes SSE lines through (OpenAI format is already the target).
func (a *OpenAIAdaptor) ConvertStreamLine(line []byte) []byte {
	return line
}

// ConvertSSEBuffer passes the SSE buffer through (already OpenAI SSE format).
func (a *OpenAIAdaptor) ConvertSSEBuffer(sseBody []byte) []byte {
	return sseBody
}

// CreateReverseStreamConverter returns nil — no reverse conversion needed for OpenAI.
func (a *OpenAIAdaptor) CreateReverseStreamConverter() func([]byte) []byte {
	return nil
}

func (a *OpenAIAdaptor) GetChannelType() string { return "openai" }

func init() {
	provider.RegisterToInternal(provider.FormatOpenAIChat, openaiChatToInternal)
	provider.RegisterFromInternal(provider.FormatOpenAIChat, internalToOpenAIChat)
	provider.RegisterToResponseInternal(provider.FormatOpenAIChat, openaiResponseToInternal)
	provider.RegisterFromResponseInternal(provider.FormatOpenAIChat, internalToOpenAIResponse)
	provider.RegisterToInternal(provider.FormatOpenAIResp, responsesToInternal)
	provider.RegisterFromInternal(provider.FormatOpenAIResp, internalToResponses)
	provider.RegisterFromResponseInternal(provider.FormatOpenAIResp, internalToResponsesResponse)
}
