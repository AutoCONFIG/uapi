package provider_conversion_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func TestChatToResponsesAlwaysEmitsInstructions(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	value, ok := got["instructions"]
	if !ok {
		t.Fatalf("instructions field missing in Responses body: %s", converted)
	}
	if value != "" {
		t.Fatalf("instructions = %#v, want empty string", value)
	}
}

func TestCrossProtocolRequestConversions(t *testing.T) {
	chat := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}],"temperature":0.2}`)
	tests := []struct {
		name   string
		target convert.Format
		want   []string
	}{
		{name: "chat to anthropic", target: convert.FormatAnthropic, want: []string{`"system"`, `"messages"`, `"max_tokens"`}},
		{name: "chat to gemini", target: convert.FormatGemini, want: []string{`"systemInstruction"`, `"contents"`}},
		{name: "chat to responses", target: convert.FormatOpenAIResponses, want: []string{`"instructions":"be brief"`, `"input"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, tt.target, chat)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			if !json.Valid(converted) {
				t.Fatalf("converted body is not valid JSON: %s", converted)
			}
			text := string(converted)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("converted body missing %s: %s", want, text)
				}
			}
		})
	}
}

func TestCrossProtocolResponseConversions(t *testing.T) {
	openAIResp := []byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	tests := []struct {
		name   string
		target convert.Format
		want   []string
	}{
		{name: "chat response to anthropic", target: convert.FormatAnthropic, want: []string{`"type":"message"`, `"content"`, `"usage"`}},
		{name: "chat response to gemini", target: convert.FormatGemini, want: []string{`"candidates"`, `"usageMetadata"`}},
		{name: "chat response to responses", target: convert.FormatOpenAIResponses, want: []string{`"object":"response"`, `"output"`, `"usage"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertResponse(convert.FormatOpenAIChatCompletions, tt.target, openAIResp)
			if err != nil {
				t.Fatalf("ConvertResponse: %v", err)
			}
			if !json.Valid(converted) {
				t.Fatalf("converted response is not valid JSON: %s", converted)
			}
			text := string(converted)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("converted response missing %s: %s", want, text)
				}
			}
		})
	}
}

func TestConvertRequestWithAdaptorUsesNewRegistry(t *testing.T) {
	adaptor := &anthropic.AnthropicAdaptor{}
	adaptor.Init(&db.Channel{Type: "anthropic", APIFormat: "standard"}, &db.Account{})
	body := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}]}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAnthropic, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	if !json.Valid(converted) {
		t.Fatalf("converted body is not valid JSON: %s", converted)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v", err)
	}
	if got["system"] != "be brief" {
		t.Fatalf("system = %#v, want be brief; body=%s", got["system"], converted)
	}
	if _, ok := got["messages"]; !ok {
		t.Fatalf("messages missing: %s", converted)
	}
}
