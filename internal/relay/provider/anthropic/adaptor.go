package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

func (a *AnthropicAdaptor) GetChannelType() string { return "anthropic" }

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

// --- Response conversion helpers ---

// ConvertAnthropicResponseToOpenAI converts a non-streaming Anthropic response to OpenAI Chat format.
func ConvertAnthropicResponseToOpenAI(respBody []byte) []byte {
	return convertAnthropicResponse(respBody)
}

// ConvertAnthropicSSEBuffer converts a full buffered Anthropic SSE body to OpenAI SSE format.
func ConvertAnthropicSSEBuffer(sseBody []byte) []byte {
	return convertAnthropicSSEBuffer(sseBody)
}

func init() {
	provider.RegisterToInternal(provider.FormatAnthropic, anthropicToInternal)
	provider.RegisterFromInternal(provider.FormatAnthropic, internalToAnthropic)
}

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*AnthropicAdaptor)(nil)

// --- Response helpers (retained from original) ---

func convertAnthropicResponse(respBody []byte) []byte {
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return respBody
	}

	msgID, _ := resp["id"].(string)
	model, _ := resp["model"].(string)
	stopReason, _ := resp["stop_reason"].(string)

	// Convert content blocks to OpenAI message
	content, toolCalls := convertContentBlocks(resp["content"])

	finishReason := mapFinishReason(stopReason)

	msg := map[string]interface{}{
		"role":    "assistant",
		"content": content,
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	usage := convertUsage(resp["usage"])

	chatResp := map[string]interface{}{
		"id":      msgID,
		"object":  "chat.completion",
		"model":   model,
		"created": time.Now().Unix(),
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
			},
		},
		"usage": usage,
	}

	b, _ := json.Marshal(chatResp)
	return b
}

func convertContentBlocks(contentRaw interface{}) (interface{}, []interface{}) {
	blocks, ok := contentRaw.([]interface{})
	if !ok {
		return nil, nil
	}

	var textParts []string
	var toolCalls []interface{}

	for _, blockRaw := range blocks {
		block, ok := blockRaw.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)

		switch blockType {
		case "text":
			if text, ok := block["text"].(string); ok {
				textParts = append(textParts, text)
			}
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			args := "{}"
			if a, err := json.Marshal(block["input"]); err == nil {
				args = string(a)
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"index": len(toolCalls),
				"id":    id,
				"type":  "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": args,
				},
			})
		}
	}

	content := strings.Join(textParts, "")
	if content == "" && len(toolCalls) == 0 {
		content = ""
	}
	return content, toolCalls
}

func convertUsage(usageRaw interface{}) map[string]interface{} {
	if usageRaw == nil {
		return map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
	}
	usage, ok := usageRaw.(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
	}
	input := toInt(usage["input_tokens"])
	output := toInt(usage["output_tokens"])
	return map[string]interface{}{
		"prompt_tokens":     input,
		"completion_tokens": output,
		"total_tokens":      input + output,
	}
}

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
