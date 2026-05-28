package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/stream"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/valyala/fasthttp"
)

type OpenAIAdaptor struct {
	channel *db.Channel
	account *db.Account
	// Cache token tracking
	lastCacheReadInputTokens int
}

func (a *OpenAIAdaptor) SetRequestParams(model string, stream bool) {
	// No-op: OpenAI does not need model/stream in URL
}

func (a *OpenAIAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *OpenAIAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(upstreamconfig.AccountEndpoint(a.channel, a.account), "/")
	if a.channel.APIFormat == "codex" && isOpenAIPlatformBaseURL(base) {
		base = CodexAPIBaseURL
	}
	if strings.HasPrefix(path, "/v1/images/") {
		return base + strings.TrimPrefix(path, "/v1"), nil
	}
	if a.channel.APIFormat == "responses" || a.channel.APIFormat == "codex" {
		return base + "/responses", nil
	}
	return base + "/chat/completions", nil
}

func (a *OpenAIAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Authorization", "Bearer "+provider.ExtractCredentialKey(credentials))
	if a.channel != nil && a.channel.APIFormat == "codex" {
		req.Header.Set("originator", CodexOriginator)
		req.Header.Set("User-Agent", CodexUserAgent)
		if accountID := metadataString(a.account, "chatgpt_account_id"); accountID != "" {
			req.Header.Set("ChatGPT-Account-ID", accountID)
		}
		if metadataBool(a.account, "chatgpt_account_is_fedramp") {
			req.Header.Set("X-OpenAI-Fedramp", "true")
		}
	}
	if len(req.Header.Peek("Content-Type")) == 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return nil
}

// --- Intermediate format conversion ---

func (a *OpenAIAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	// Determine format based on API format
	format := convert.FormatOpenAIChatCompletions
	if a.channel != nil && (a.channel.APIFormat == "responses" || a.channel.APIFormat == "codex") {
		format = convert.FormatOpenAIResponses
	}
	ir, err := convert.ToInternalOnly(format, body)
	if err != nil {
		return nil, err
	}
	return convert.ToProviderInternal(ir), nil
}

func (a *OpenAIAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	format := convert.FormatOpenAIChatCompletions
	if a.channel != nil && (a.channel.APIFormat == "responses" || a.channel.APIFormat == "codex") {
		format = convert.FormatOpenAIResponses
	}
	// Convert provider.InternalRequest to convert.InternalRequest for conversion
	ir := convert.FromProviderInternal(req)
	fromInternal, ok := convert.GetFromInternalFunc(format)
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format %q", format)
	}
	return fromInternal(ir)
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
	var resp struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse openai response: %w", err)
	}
	pt, ct := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	if pt == 0 && ct == 0 {
		pt, ct = resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	a.lastCacheReadInputTokens = resp.Usage.PromptTokensDetails.CachedTokens
	return pt, ct, nil
}

// ParseUsageFull returns full usage including cache tokens.
func (a *OpenAIAdaptor) ParseUsageFull(respBody []byte) (provider.InternalUsage, error) {
	pt, ct, err := a.ParseUsage(respBody)
	if err != nil {
		return provider.InternalUsage{}, err
	}
	return provider.InternalUsage{
		PromptTokens:         pt,
		CompletionTokens:     ct,
		CacheReadInputTokens: a.lastCacheReadInputTokens,
	}, nil
}

func (a *OpenAIAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
		Response struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(lastChunk, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse openai stream usage: %w", err)
	}
	pt, ct := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	if pt == 0 && ct == 0 {
		pt, ct = resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	if pt == 0 && ct == 0 {
		pt, ct = resp.Response.Usage.InputTokens, resp.Response.Usage.OutputTokens
	}
	return pt, ct, nil
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
	if a.channel == nil || (a.channel.APIFormat != "responses" && a.channel.APIFormat != "codex") {
		return nil
	}
	// Convert from OpenAI Responses (upstream) to Chat Completions (client)
	upstream := convert.FormatOpenAIResponses
	client := convert.FormatOpenAIChatCompletions
	converter := stream.NewConverter(upstream, client)
	if converter == nil {
		return func(line []byte) []byte { return line }
	}
	return converter.Convert
}

func (a *OpenAIAdaptor) GetChannelType() string { return "openai" }

func isOpenAIPlatformBaseURL(base string) bool {
	base = strings.TrimRight(strings.ToLower(strings.TrimSpace(base)), "/")
	return base == "" || base == "https://api.openai.com" || base == "https://api.openai.com/v1"
}

func metadataString(account *db.Account, key string) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	value, _ := account.Metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataBool(account *db.Account, key string) bool {
	if account == nil || account.Metadata == nil {
		return false
	}
	value, _ := account.Metadata[key].(bool)
	return value
}

func init() {
	provider.RegisterToInternal(provider.FormatOpenAIChatCompletions, openaiChatToInternal)
	provider.RegisterFromInternal(provider.FormatOpenAIChatCompletions, internalToOpenAIChat)
	provider.RegisterToResponseInternal(provider.FormatOpenAIChatCompletions, openaiResponseToInternal)
	provider.RegisterFromResponseInternal(provider.FormatOpenAIChatCompletions, internalToOpenAIResponse)
	provider.RegisterToInternal(provider.FormatOpenAIResponses, responsesToInternal)
	provider.RegisterFromInternal(provider.FormatOpenAIResponses, internalToResponses)
	provider.RegisterToResponseInternal(provider.FormatOpenAIResponses, responsesResponseToInternal)
	provider.RegisterFromResponseInternal(provider.FormatOpenAIResponses, internalToResponsesResponse)
}
