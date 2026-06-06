package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
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
	if openAIPassthroughPath(path) {
		return base + strings.TrimPrefix(path, "/v1"), nil
	}
	if isOpenAIResponsesAPIFormat(a.channel.APIFormat) || isCodexResponsesAPIFormat(a.channel.APIFormat) {
		return base + "/responses", nil
	}
	return base + "/chat/completions", nil
}

func openAIPassthroughPath(path string) bool {
	return strings.HasPrefix(path, "/v1/images/") ||
		strings.HasPrefix(path, "/v1/audio/") ||
		strings.HasPrefix(path, "/v1/embeddings") ||
		strings.HasPrefix(path, "/v1/moderations") ||
		strings.HasPrefix(path, "/v1/realtime/") ||
		strings.HasPrefix(path, "/v1/videos") ||
		strings.HasPrefix(path, "/v1/video/")
}

func (a *OpenAIAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Authorization", "Bearer "+provider.ExtractCredentialKey(credentials))
	if a.channel != nil && isCodexResponsesAPIFormat(a.channel.APIFormat) {
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

func (a *OpenAIAdaptor) FromIR(req *ir.Request) ([]byte, error) {
	format := convert.FormatOpenAIChatCompletions
	if a.channel != nil && (isOpenAIResponsesAPIFormat(a.channel.APIFormat) || isCodexResponsesAPIFormat(a.channel.APIFormat)) {
		format = convert.FormatOpenAIResponses
		if isCodexResponsesAPIFormat(a.channel.APIFormat) {
			format = convert.FormatCodexResponses
		}
	}
	return convert.FromIR(req, format)
}

// --- Response/stream handling ---

type openAIUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	TotalTokens          int `json:"total_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
}

type openAIResponse struct {
	Usage openAIUsage `json:"usage"`
}

func (a *OpenAIAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			PromptTokens         int `json:"prompt_tokens"`
			CompletionTokens     int `json:"completion_tokens"`
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CachedTokens         int `json:"cached_tokens"`
			PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
			PromptTokensDetails  struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse openai response: %w", err)
	}
	pt, ct := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	if pt == 0 && ct == 0 {
		pt, ct = resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	a.lastCacheReadInputTokens = firstPositiveInt(
		resp.Usage.PromptTokensDetails.CachedTokens,
		resp.Usage.InputTokensDetails.CachedTokens,
		resp.Usage.CachedTokens,
		resp.Usage.PromptCacheHitTokens,
	)
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

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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

func (a *OpenAIAdaptor) GetChannelType() string { return "openai" }

func isOpenAIPlatformBaseURL(base string) bool {
	base = strings.TrimRight(strings.ToLower(strings.TrimSpace(base)), "/")
	return base == "" || base == "https://api.openai.com" || base == "https://api.openai.com/v1"
}

func isOpenAIResponsesAPIFormat(format string) bool {
	return format == "responses"
}

func isCodexResponsesAPIFormat(format string) bool {
	return format == "codex" || format == "codex_apikey"
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
