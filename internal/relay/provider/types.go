package provider

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/valyala/fasthttp"
)

type Format string

const (
	FormatOpenAIChatCompletions Format = "openai_chat"
	FormatOpenAIResponses       Format = "openai_responses"
	FormatCodexResponses        Format = "codex"
	FormatAnthropic             Format = "anthropic"
	FormatClaudeCode            Format = "claude_code"
	FormatGemini                Format = "gemini"
	FormatGeminiCode            Format = "gemini_code"
	FormatGeminiCLI             Format = "gemini_cli" // Gemini CLI / Antigravity protocol
	FormatAntigravity           Format = "antigravity"
)

type InternalUsage struct {
	PromptTokens             int
	CompletionTokens         int
	CacheCreationInputTokens int // Anthropic cache_creation_input_tokens
	CacheReadInputTokens     int // Anthropic cache_read_input_tokens / OpenAI cached_tokens
	PromptCacheHitTokens     int // Provider-specific cache hit alias, normalized into cache read
	PromptTokensDetails      map[string]interface{}
	CompletionTokensDetails  map[string]interface{}
}

// ToInt converts an interface{} (float64, int, etc.) to int.
func ToInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	default:
		return 0
	}
	return 0
}

// DecodeJSONUseNumber decodes JSON into interface maps without coercing numbers
// to float64, preserving tool-call argument precision across protocol conversion.
func DecodeJSONUseNumber(body []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	return dec.Decode(v)
}

func ToFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// RandomHex generates a random hex string of n bytes using crypto/rand.
// Falls back to a timestamp-based hex if crypto/rand fails.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		ts := fmt.Sprintf("%x", time.Now().UnixNano())
		for len(ts) < n*2 {
			ts += ts
		}
		return ts[:n*2]
	}
	return hex.EncodeToString(b)
}

type Adaptor interface {
	Init(channel *db.Channel, account *db.Account)
	SetRequestParams(model string, stream bool)
	GetRequestURL(path string) (string, error)
	SetupRequestHeader(req *fasthttp.Request, credentials string) error
	ToIR(body []byte) (*ir.Request, error)
	FromIR(req *ir.Request) ([]byte, error)
	ParseUsage(respBody []byte) (promptTokens, completionTokens int, err error)
	ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
	// ParseUsageFull returns full usage including cache tokens
	ParseUsageFull(respBody []byte) (InternalUsage, error)
	GetChannelType() string
}
