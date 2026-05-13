package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

type GeminiAdaptor struct {
	channel *db.Channel
	account *db.Account
}

func (a *GeminiAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *GeminiAdaptor) GetRequestURL(path string) (string, error) {
	// Will append ?key= in SetupRequestHeader instead of header
	base := strings.TrimRight(a.channel.Endpoint, "/")
	return base + path, nil
}

func (a *GeminiAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	// Gemini uses query param for auth
	uri := req.URI()
	uri.QueryArgs().Set("key", credentials)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func (a *GeminiAdaptor) ConvertRequest(body []byte) ([]byte, error) {
	// TODO: convert OpenAI format to Gemini format if needed
	return body, nil
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiResponse struct {
	UsageMetadata geminiUsage `json:"usageMetadata"`
}

func (a *GeminiAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp geminiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse gemini response: %w", err)
	}
	return resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount, nil
}

func (a *GeminiAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	var resp geminiResponse
	if err := json.Unmarshal(lastChunk, &resp); err != nil {
		return 0, 0, nil
	}
	return resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount, nil
}

func (a *GeminiAdaptor) GetChannelType() string { return "gemini" }
