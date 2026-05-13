package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

type OpenAIAdaptor struct {
	channel *db.Channel
	account *db.Account
}

func (a *OpenAIAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *OpenAIAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(a.channel.Endpoint, "/")
	return base + path, nil
}

func (a *OpenAIAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Authorization", "Bearer "+credentials)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func (a *OpenAIAdaptor) ConvertRequest(body []byte) ([]byte, error) {
	// OpenAI format passthrough — no conversion needed
	return body, nil
}

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

func (a *OpenAIAdaptor) GetChannelType() string { return "openai" }
