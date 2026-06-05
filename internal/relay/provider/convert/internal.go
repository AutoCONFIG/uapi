package convert

import (
	"bytes"
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

// Format identifies a protocol format for conversion dispatch.
type Format string

const (
	FormatOpenAIChatCompletions Format = "openai_chat"
	FormatOpenAIResponses       Format = "openai_responses"
	FormatCodexResponses        Format = "codex"
	FormatChatGPTReverse        Format = "chatgpt_reverse"
	FormatAnthropic             Format = "anthropic"
	FormatClaudeCode            Format = "claude_code"
	FormatGemini                Format = "gemini"
	FormatGeminiCode            Format = "gemini_code"
	FormatGeminiCLI             Format = "gemini_cli"
	FormatAntigravity           Format = "antigravity"
)

func parseOpenAIChatRequestIR(body []byte) (*ir.Request, error) {
	return parseOpenAIChatRequestDirectIR(body)
}

func emitOpenAIChatRequestIR(req *ir.Request) ([]byte, error) {
	return emitOpenAIChatRequestDirectIR(req)
}

func parseOpenAIResponsesRequestIR(body []byte) (*ir.Request, error) {
	req, err := parseOpenAIResponsesRequestDirectIR(body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func emitOpenAIResponsesRequestIR(req *ir.Request) ([]byte, error) {
	return emitOpenAIResponsesRequestDirectIR(req)
}

func parseAnthropicRequestIR(body []byte) (*ir.Request, error) {
	return parseAnthropicRequestDirectIR(body)
}

func emitAnthropicRequestIR(req *ir.Request) ([]byte, error) {
	return emitAnthropicRequestDirectIR(req)
}

func parseGeminiRequestIR(body []byte) (*ir.Request, error) {
	return parseGeminiRequestDirectIR(body)
}

func emitGeminiRequestIR(req *ir.Request) ([]byte, error) {
	return emitGeminiRequestDirectIR(req)
}

func parseGeminiCLIRequestIR(body []byte) (*ir.Request, error) {
	return parseGeminiCLIRequestDirectIR(body)
}

func emitGeminiCLIRequestIR(req *ir.Request) ([]byte, error) {
	return emitGeminiCLIRequestDirectIR(req)
}

func parseOpenAIChatResponseIR(body []byte) (*ir.Response, error) {
	return parseOpenAIChatResponseDirectIR(body)
}

func emitOpenAIChatResponseIR(resp *ir.Response) ([]byte, error) {
	return emitOpenAIChatResponseDirectIR(resp)
}

func parseOpenAIResponsesResponseIR(body []byte) (*ir.Response, error) {
	return parseOpenAIResponsesResponseDirectIR(body)
}

func emitOpenAIResponsesResponseIR(resp *ir.Response) ([]byte, error) {
	return emitOpenAIResponsesResponseDirectIR(resp)
}

func parseAnthropicResponseIR(body []byte) (*ir.Response, error) {
	return parseAnthropicResponseDirectIR(body)
}

func emitAnthropicResponseIR(resp *ir.Response) ([]byte, error) {
	return emitAnthropicResponseDirectIR(resp)
}

func parseGeminiResponseIR(body []byte) (*ir.Response, error) {
	return parseGeminiResponseDirectIR(body)
}

func emitGeminiResponseIR(resp *ir.Response) ([]byte, error) {
	return emitGeminiResponseDirectIR(resp)
}

func parseGeminiCLIResponseIR(body []byte) (*ir.Response, error) {
	return parseGeminiCLIResponseDirectIR(body)
}

func emitGeminiCLIResponseIR(resp *ir.Response) ([]byte, error) {
	return emitGeminiCLIResponseDirectIR(resp)
}

func decodeJSONUseNumber(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

func copyRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
