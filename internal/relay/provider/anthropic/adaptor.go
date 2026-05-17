package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

type AnthropicAdaptor struct {
	channel     *db.Channel
	account     *db.Account
	stream      bool
	streamState *anthropicStreamState
}

func (a *AnthropicAdaptor) SetRequestParams(model string, stream bool) {
	// No-op: Anthropic does not need model/stream in URL
}

func (a *AnthropicAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
	a.streamState = &anthropicStreamState{}
}

func (a *AnthropicAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(a.channel.Endpoint, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		return base + "/v1/messages", nil
	}
	return base + path, nil
}

func (a *AnthropicAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	req.Header.Set("x-api-key", provider.ExtractCredentialKey(credentials))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// --- Intermediate format conversion ---

func (a *AnthropicAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	return anthropicToInternal(body)
}

func (a *AnthropicAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	return internalToAnthropic(req)
}

// --- Streaming ---

// ConvertStreamLine converts a single Anthropic SSE line to OpenAI SSE format in real-time.
func (a *AnthropicAdaptor) ConvertStreamLine(line []byte) []byte {
	return a.streamState.convertLine(line)
}

func (a *AnthropicAdaptor) ConvertSSEBuffer(sseBody []byte) []byte {
	return convertAnthropicSSEBuffer(sseBody)
}

func (a *AnthropicAdaptor) GetChannelType() string { return "anthropic" }

// CreateReverseStreamConverter returns a stateful converter that converts OpenAI SSE chunks
// back to Anthropic SSE events for clients requesting Anthropic format.
func (a *AnthropicAdaptor) CreateReverseStreamConverter() func([]byte) []byte {
	state := newAnthropicReverseState()
	return state.convertReverseLine
}

// --- Usage parsing ---

// ParseUsage parses non-streaming Anthropic response usage.
func (a *AnthropicAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, 0, fmt.Errorf("parse anthropic response: %w", err)
	}
	return resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
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
	if json.Unmarshal(lastChunk, &resp) == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
	}

	// Try raw Anthropic format (message_delta event)
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
	if json.Unmarshal(lastChunk, &event) == nil {
		return event.Message.Usage.InputTokens, event.Usage.OutputTokens, nil
	}
	return 0, 0, nil
}

func init() {
	provider.RegisterToInternal(provider.FormatAnthropic, anthropicToInternal)
	provider.RegisterFromInternal(provider.FormatAnthropic, internalToAnthropic)
	provider.RegisterToResponseInternal(provider.FormatAnthropic, anthropicResponseToInternal)
	provider.RegisterFromResponseInternal(provider.FormatAnthropic, internalToAnthropicResponse)
}

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*AnthropicAdaptor)(nil)

// toInt converts an interface{} (float64, int, etc.) to int.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// mapFinishReason maps Anthropic stop_reason to OpenAI finish_reason (used by streaming).
func mapFinishReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
