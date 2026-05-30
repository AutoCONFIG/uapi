package gemini

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

type GeminiAdaptor struct {
	channel  *db.Channel
	account  *db.Account
	model    string
	isStream bool
	// Cache token tracking
	lastCacheCreationInputTokens int
	lastCacheReadInputTokens     int
}

func (a *GeminiAdaptor) SetRequestParams(model string, stream bool) {
	a.model = model
	a.isStream = stream
}

func (a *GeminiAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *GeminiAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(upstreamconfig.AccountEndpoint(a.channel, a.account), "/")
	if a.channel.APIFormat == "gemini_code" {
		return codeAssistBase(base) + "/v1internal:generateContent", nil
	}
	// URL will be rewritten in SetupRequestHeader after model is known
	return base + "/placeholder", nil
}

func (a *GeminiAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Content-Type", "application/json")

	// Build Gemini API URL
	base := strings.TrimRight(upstreamconfig.AccountEndpoint(a.channel, a.account), "/")
	if a.channel.APIFormat == "gemini_code" {
		action := "generateContent"
		suffix := ""
		if a.isStream {
			action = "streamGenerateContent"
			suffix = "?alt=sse"
		}
		req.SetRequestURI(codeAssistBase(base) + "/v1internal:" + action + suffix)
		credential := provider.ExtractCredentialKey(credentials)
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("User-Agent", GeminiCLIUserAgent(resolveCodeAssistModel(a.model)))
		return nil
	}
	action := "generateContent"
	suffix := ""
	if a.isStream {
		action = "streamGenerateContent"
		suffix = "?alt=sse"
	}
	if a.model != "" {
		req.SetRequestURI(base + "/models/" + a.model + ":" + action + suffix)
	}

	// Set API key AFTER SetRequestURI so it's not overwritten
	credential := provider.ExtractCredentialKey(credentials)
	if a.account != nil && a.account.CredType == "oauth_token" {
		req.Header.Set("Authorization", "Bearer "+credential)
		return nil
	}
	req.URI().QueryArgs().Set("key", credential)
	return nil
}

// --- Intermediate format conversion ---

func (a *GeminiAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	format := convert.FormatGemini
	if a.channel != nil && a.channel.APIFormat == "gemini_code" {
		format = convert.FormatGeminiCLI
	}
	ir, err := convert.ToInternalOnly(format, body)
	if err != nil {
		return nil, err
	}
	return ir, nil
}

func (a *GeminiAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	// Store model and stream for URL construction
	a.model = req.Model
	a.isStream = req.Stream
	if a.channel != nil && a.channel.APIFormat == "gemini_code" {
		return internalToGeminiCodeAssistWithAccount(req, a.account)
	}
	fromInternal, ok := convert.GetFromInternalFunc(convert.FormatGemini)
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format %q", convert.FormatGemini)
	}
	return fromInternal(req)
}

func (a *GeminiAdaptor) GetChannelType() string { return "gemini" }

// --- Usage parsing ---

// ParseUsage parses non-streaming Gemini response usage.
func (a *GeminiAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp struct {
		Response struct {
			UsageMetadata struct {
				PromptTokenCount        int `json:"promptTokenCount"`
				CandidatesTokenCount    int `json:"candidatesTokenCount"`
				CachedContentTokenCount int `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse gemini response: %w", err)
	}
	if resp.Response.UsageMetadata.PromptTokenCount > 0 || resp.Response.UsageMetadata.CandidatesTokenCount > 0 {
		a.lastCacheReadInputTokens = resp.Response.UsageMetadata.CachedContentTokenCount
		return resp.Response.UsageMetadata.PromptTokenCount, resp.Response.UsageMetadata.CandidatesTokenCount, nil
	}
	a.lastCacheReadInputTokens = resp.UsageMetadata.CachedContentTokenCount
	return resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount, nil
}

// ParseUsageFull returns full usage including cache tokens.
func (a *GeminiAdaptor) ParseUsageFull(respBody []byte) (provider.InternalUsage, error) {
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

// ParseStreamUsage parses the last chunk for usage data.
func (a *GeminiAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	// Try OpenAI format first (after conversion)
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(lastChunk, &resp); err == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
	}

	// Try raw Gemini format
	var gemResp struct {
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(lastChunk, &gemResp); err == nil {
		return gemResp.UsageMetadata.PromptTokenCount, gemResp.UsageMetadata.CandidatesTokenCount, nil
	}
	return 0, 0, fmt.Errorf("parse gemini stream usage: no recognized format")
}

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*GeminiAdaptor)(nil)

func codeAssistBase(base string) string {
	if base == "" || strings.Contains(base, "generativelanguage.googleapis.com") {
		return "https://cloudcode-pa.googleapis.com"
	}
	return strings.TrimRight(base, "/")
}
