package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

type GeminiAdaptor struct {
	channel     *db.Channel
	account     *db.Account
	model       string
	isStream    bool
	streamState *geminiStreamState
}

func (a *GeminiAdaptor) SetRequestParams(model string, stream bool) {
	a.model = model
	a.isStream = stream
}

func (a *GeminiAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
	a.streamState = &geminiStreamState{}
}

func (a *GeminiAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(a.channel.Endpoint, "/")
	if a.channel.APIFormat == "gemini_code" {
		return codeAssistBase(base) + "/v1internal:generateContent", nil
	}
	// URL will be rewritten in SetupRequestHeader after model is known
	return base + "/placeholder", nil
}

func (a *GeminiAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("Content-Type", "application/json")

	// Build Gemini API URL
	base := strings.TrimRight(a.channel.Endpoint, "/")
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
		req.Header.Set("User-Agent", "GeminiCLI/unknown/"+resolveCodeAssistModel(a.model)+" (linux; amd64; cli)")
		return nil
	}
	action := "generateContent"
	suffix := ""
	if a.isStream {
		action = "streamGenerateContent"
		suffix = "?alt=sse"
	}
	if a.model != "" {
		req.SetRequestURI(base + "/v1beta/models/" + a.model + ":" + action + suffix)
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
	return geminiToInternal(body)
}

func (a *GeminiAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	// Store model and stream for URL construction
	a.model = req.Model
	a.isStream = req.Stream
	if a.channel.APIFormat == "gemini_code" {
		return internalToGeminiCodeAssistWithAccount(req, a.account)
	}
	return internalToGemini(req)
}

// --- Streaming ---

// ConvertStreamLine converts a single Gemini SSE/JSON line to OpenAI SSE format.
func (a *GeminiAdaptor) ConvertStreamLine(line []byte) []byte {
	if a.channel != nil && a.channel.APIFormat == "gemini_code" {
		return a.streamState.convertLine(unwrapCodeAssistSSELine(line), a.model)
	}
	return a.streamState.convertLine(line, a.model)
}

func (a *GeminiAdaptor) GetChannelType() string { return "gemini" }

// CreateReverseStreamConverter returns a stateful converter that converts OpenAI SSE chunks
// back to Gemini SSE format for clients requesting Gemini format.
func (a *GeminiAdaptor) CreateReverseStreamConverter() func([]byte) []byte {
	state := newGeminiReverseState()
	return state.convertReverseLine
}

// --- Usage parsing ---

// ParseUsage parses non-streaming Gemini response usage.
func (a *GeminiAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp struct {
		Response struct {
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse gemini response: %w", err)
	}
	if resp.Response.UsageMetadata.PromptTokenCount > 0 || resp.Response.UsageMetadata.CandidatesTokenCount > 0 {
		return resp.Response.UsageMetadata.PromptTokenCount, resp.Response.UsageMetadata.CandidatesTokenCount, nil
	}
	return resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount, nil
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
	if json.Unmarshal(lastChunk, &resp) == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
	}

	// Try raw Gemini format
	var gemResp struct {
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(lastChunk, &gemResp) == nil {
		return gemResp.UsageMetadata.PromptTokenCount, gemResp.UsageMetadata.CandidatesTokenCount, nil
	}
	return 0, 0, nil
}

func init() {
	provider.RegisterToInternal(provider.FormatGemini, geminiToInternal)
	provider.RegisterFromInternal(provider.FormatGemini, internalToGemini)
	provider.RegisterToResponseInternal(provider.FormatGemini, geminiResponseToInternal)
	provider.RegisterFromResponseInternal(provider.FormatGemini, internalToGeminiResponse)
	provider.RegisterToInternal(provider.FormatGeminiCode, geminiToInternal)
	provider.RegisterFromInternal(provider.FormatGeminiCode, internalToGeminiCodeAssist)
	provider.RegisterToResponseInternal(provider.FormatGeminiCode, geminiCodeAssistResponseToInternal)
}

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*GeminiAdaptor)(nil)

func (a *GeminiAdaptor) ConvertSSEBuffer(sseBody []byte) []byte {
	if a.channel != nil && a.channel.APIFormat == "gemini_code" {
		return convertGeminiSSEBuffer(unwrapCodeAssistSSEBuffer(sseBody))
	}
	return convertGeminiSSEBuffer(sseBody)
}

func mapGeminiFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return "stop"
	}
}

func codeAssistBase(base string) string {
	if base == "" || strings.Contains(base, "generativelanguage.googleapis.com") {
		return "https://cloudcode-pa.googleapis.com"
	}
	return strings.TrimRight(base, "/")
}

func unwrapCodeAssistSSELine(line []byte) []byte {
	const prefix = "data:"
	text := string(line)
	if !strings.HasPrefix(text, prefix) {
		return line
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, prefix))
	if payload == "" || payload == "[DONE]" {
		return line
	}
	var wrapper map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &wrapper); err != nil {
		return line
	}
	resp, ok := wrapper["response"]
	if !ok {
		return line
	}
	respBody, err := json.Marshal(resp)
	if err != nil {
		return line
	}
	return []byte("data: " + string(respBody))
}

func unwrapCodeAssistSSEBuffer(sseBody []byte) []byte {
	lines := strings.Split(string(sseBody), "\n")
	for i, line := range lines {
		lines[i] = string(unwrapCodeAssistSSELine([]byte(line)))
	}
	return []byte(strings.Join(lines, "\n"))
}
