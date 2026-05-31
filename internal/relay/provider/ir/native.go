package ir

import "encoding/json"

type Protocol string

const (
	ProtocolOpenAIChat      Protocol = "openai_chat"
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolCodex           Protocol = "codex"
	ProtocolAnthropic       Protocol = "anthropic"
	ProtocolClaudeCode      Protocol = "claude_code"
	ProtocolGemini          Protocol = "gemini"
	ProtocolGeminiCode      Protocol = "gemini_code"
	ProtocolGeminiCLI       Protocol = "gemini_cli"
	ProtocolAntigravity     Protocol = "antigravity"
)

// NativeEnvelope preserves the source protocol shape for lossless same-protocol
// replay and for fields that do not have a protocol-neutral home yet.
type NativeEnvelope struct {
	Protocol Protocol                   `json:"protocol,omitempty"`
	Kind     string                     `json:"kind,omitempty"`
	Raw      json.RawMessage            `json:"raw,omitempty"`
	RawBody  json.RawMessage            `json:"raw_body,omitempty"`
	Fields   map[string]json.RawMessage `json:"fields,omitempty"`
	Unknown  map[string]json.RawMessage `json:"unknown,omitempty"`
	Headers  map[string]string          `json:"headers,omitempty"`
	Meta     map[string]json.RawMessage `json:"meta,omitempty"`
	Index    int                        `json:"index,omitempty"`
}

func CloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func CloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		out[key] = CloneRaw(value)
	}
	return out
}
