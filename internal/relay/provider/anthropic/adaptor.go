package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/valyala/fasthttp"
)

type AnthropicAdaptor struct {
	channel *db.Channel
	account *db.Account
	// Cache token tracking
	lastCacheCreationInputTokens int
	lastCacheReadInputTokens     int
}

func (a *AnthropicAdaptor) SetRequestParams(model string, stream bool) {
	// No-op: Anthropic does not need model/stream in URL
}

func (a *AnthropicAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *AnthropicAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(upstreamconfig.AccountEndpoint(a.channel, a.account), "/")
	return base + "/messages", nil
}

func (a *AnthropicAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	credential := provider.ExtractCredentialKey(credentials)
	if a.account != nil && a.account.CredType == "oauth_token" {
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("anthropic-beta", OAuthBetaHeader)
		req.Header.Set("x-app", "cli")
		req.Header.Set("User-Agent", ClaudeCLIUserAgent)
		req.Header.Set("X-Claude-Code-Session-Id", ClaudeCodeSessionID)
	} else {
		req.Header.Set("x-api-key", credential)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// --- Intermediate format conversion ---

func (a *AnthropicAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	ir, err := convert.ToInternalOnly(convert.FormatAnthropic, body)
	if err != nil {
		return nil, err
	}
	return provider.ToProviderInternal(ir), nil
}

func (a *AnthropicAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	ir := provider.FromProviderInternal(req)
	fromInternal, ok := convert.GetFromInternalFunc(convert.FormatAnthropic)
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format %q", convert.FormatAnthropic)
	}
	return fromInternal(ir)
}

func (a *AnthropicAdaptor) GetChannelType() string { return "anthropic" }

// --- Usage parsing ---

// ParseUsage parses non-streaming Anthropic response usage.
func (a *AnthropicAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse anthropic response: %w", err)
	}
	// Store cache tokens in adaptor for later retrieval
	a.lastCacheCreationInputTokens = resp.Usage.CacheCreationInputTokens
	a.lastCacheReadInputTokens = resp.Usage.CacheReadInputTokens
	return resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
}

// GetCacheTokens returns the last seen cache token counts.
func (a *AnthropicAdaptor) GetCacheTokens() (cacheCreationInputTokens, cacheReadInputTokens int) {
	return a.lastCacheCreationInputTokens, a.lastCacheReadInputTokens
}

// ParseUsageFull returns full usage including cache tokens.
func (a *AnthropicAdaptor) ParseUsageFull(respBody []byte) (provider.InternalUsage, error) {
	pt, ct, err := a.ParseUsage(respBody)
	if err != nil {
		return provider.InternalUsage{}, err
	}
	return provider.InternalUsage{
		PromptTokens:             pt,
		CompletionTokens:         ct,
		CacheCreationInputTokens: a.lastCacheCreationInputTokens,
		CacheReadInputTokens:     a.lastCacheReadInputTokens,
	}, nil
}

// ParseStreamUsage parses the last SSE chunk for usage data.
// For Anthropic streams, usage comes in message_delta event.
func (a *AnthropicAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	// The lastChunk might be a converted OpenAI chunk or raw Anthropic event
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(lastChunk, &resp); err == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
	}

	// Try raw Anthropic message_start event (carries input_tokens)
	var msgStart struct {
		Type    string `json:"type"`
		Message struct {
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(lastChunk, &msgStart); err == nil && msgStart.Type == "message_start" && msgStart.Message.Usage.InputTokens > 0 {
		return msgStart.Message.Usage.InputTokens, 0, nil
	}

	// Try raw Anthropic message_delta event (carries output_tokens)
	var event struct {
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Message struct {
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(lastChunk, &event); err == nil {
		return event.Message.Usage.InputTokens, event.Usage.OutputTokens, nil
	}
	return 0, 0, fmt.Errorf("parse anthropic stream usage: no recognized format")
}

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*AnthropicAdaptor)(nil)
