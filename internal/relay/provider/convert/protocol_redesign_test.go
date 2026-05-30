package convert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
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

func TestOpenAIChatToolChoiceToGeminiDoesNotEmitEmptyMode(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}],"tool_choice":{"type":"auto"}}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	tools, ok := got["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing: %s", converted)
	}
	functionDeclarations, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(functionDeclarations) != 1 {
		t.Fatalf("functionDeclarations missing: %s", converted)
	}
	declaration := functionDeclarations[0].(map[string]interface{})
	if declaration["name"] != "lookup" {
		t.Fatalf("function name = %#v, want lookup; body=%s", declaration["name"], converted)
	}
	if _, ok := declaration["parametersJsonSchema"]; !ok {
		t.Fatalf("parametersJsonSchema missing: %s", converted)
	}
	if _, ok := declaration["parameters"]; ok {
		t.Fatalf("parameters should not be emitted for OpenAI function tools: %s", converted)
	}
	toolConfig, ok := got["toolConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("toolConfig missing: %s", converted)
	}
	fcConfig, ok := toolConfig["functionCallingConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("functionCallingConfig missing: %s", converted)
	}
	if fcConfig["mode"] != "AUTO" {
		t.Fatalf("mode = %#v, want AUTO; body=%s", fcConfig["mode"], converted)
	}
}

func TestOpenAIChatToGeminiNormalizesRolesForPrivateAPIs(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"tool","tool_call_id":"lookup","content":"done"}]}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got struct {
		Contents []struct {
			Role string `json:"role"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	want := []string{"user", "model", "user"}
	if len(got.Contents) != len(want) {
		t.Fatalf("contents length = %d, want %d: %s", len(got.Contents), len(want), converted)
	}
	for i, role := range want {
		if got.Contents[i].Role != role {
			t.Fatalf("contents[%d].role = %q, want %q; body=%s", i, got.Contents[i].Role, role, converted)
		}
	}
}

func TestAntigravityAdaptorToolChoiceToGeminiDoesNotEmitEmptyMode(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}],"tool_choice":{"type":"auto"}}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	tools, ok := request["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing: %s", converted)
	}
	functionDeclarations, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(functionDeclarations) != 1 {
		t.Fatalf("functionDeclarations missing: %s", converted)
	}
	toolConfig, ok := request["toolConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("toolConfig missing: %s", converted)
	}
	fcConfig, ok := toolConfig["functionCallingConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("functionCallingConfig missing: %s", converted)
	}
	if fcConfig["mode"] != "AUTO" {
		t.Fatalf("mode = %#v, want AUTO; body=%s", fcConfig["mode"], converted)
	}
}

func TestAntigravityAdaptorNormalizesGeminiRoles(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request := got["request"].(map[string]interface{})
	contents := request["contents"].([]interface{})
	second := contents[1].(map[string]interface{})
	if second["role"] != "model" {
		t.Fatalf("assistant role should be model for antigravity: %s", converted)
	}
}

func TestAntigravityAdaptorWithoutToolChoiceOmitsToolConfig(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"}],"stream":true}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	if _, ok := request["toolConfig"]; ok {
		t.Fatalf("toolConfig should be omitted without tool_choice/tools: %s", converted)
	}
}

func TestOpenAIChatToolChoiceWithoutToolsOmitsGeminiToolConfig(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"tool_choice":{"type":"auto"}}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["toolConfig"]; ok {
		t.Fatalf("toolConfig should be omitted without tools: %s", converted)
	}
}

func TestCrossProtocolRequestConversionDropsSourceExtraFields(t *testing.T) {
	responsesBody := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["reasoning.encrypted_content"],
		"previous_response_id":"resp_1",
		"conversation":"conv_1",
		"metadata":{"trace":"abc"},
		"prompt_cache_key":"cache",
		"safety_identifier":"safe",
		"custom_source_only":true
	}`)
	chatBody := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"custom_source_only":true
	}`)

	tests := []struct {
		name   string
		source convert.Format
		target convert.Format
		body   []byte
		absent []string
	}{
		{
			name:   "responses to gemini",
			source: convert.FormatOpenAIResponses,
			target: convert.FormatGemini,
			body:   responsesBody,
			absent: []string{"include", "previous_response_id", "conversation", "metadata", "prompt_cache_key", "safety_identifier", "custom_source_only"},
		},
		{
			name:   "responses to anthropic",
			source: convert.FormatOpenAIResponses,
			target: convert.FormatAnthropic,
			body:   responsesBody,
			absent: []string{"include", "previous_response_id", "conversation", "metadata", "prompt_cache_key", "safety_identifier", "custom_source_only"},
		},
		{
			name:   "chat to gemini",
			source: convert.FormatOpenAIChatCompletions,
			target: convert.FormatGemini,
			body:   chatBody,
			absent: []string{"custom_source_only"},
		},
		{
			name:   "gemini to chat",
			source: convert.FormatGemini,
			target: convert.FormatOpenAIChatCompletions,
			body:   []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"custom_source_only":true}`),
			absent: []string{"custom_source_only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertRequest(tt.source, tt.target, tt.body)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(converted, &got); err != nil {
				t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
			}
			for _, key := range tt.absent {
				if containsJSONKey(got, key) {
					t.Fatalf("source-only key %q leaked into converted body: %s", key, converted)
				}
			}
		})
	}
}

func TestGeminiCLIEnvelopeExtraFieldsStayInsideCLIEnvelope(t *testing.T) {
	body := []byte(`{
		"model":"gemini-2.5-pro",
		"project":"project-1",
		"userAgent":"gemini-cli",
		"requestType":"generateContent",
		"requestId":"req-1",
		"sessionId":"sess-1",
		"request":{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"custom_inner_only":true}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGeminiCLI, convert.FormatGeminiCLI, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if got["project"] != "project-1" || got["userAgent"] != "gemini-cli" || got["requestType"] != "generateContent" || got["sessionId"] != "sess-1" {
		t.Fatalf("CLI envelope fields not preserved: %s", converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	if containsJSONKey(request, "project") || containsJSONKey(request, "userAgent") || containsJSONKey(request, "requestType") || containsJSONKey(request, "sessionId") {
		t.Fatalf("CLI envelope fields leaked into inner Gemini request: %s", converted)
	}
	if _, ok := request["custom_inner_only"]; !ok {
		t.Fatalf("same-format Gemini CLI inner extra should be preserved: %s", converted)
	}
}

func TestAdaptorCrossProtocolConversionDropsSourceExtraFields(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{
		"model":"gpt-oss-120b-medium",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"conversation":"conv_1",
		"metadata":{"trace":"abc"},
		"prompt_cache_key":"cache",
		"custom_source_only":true
	}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIResponses, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	for _, key := range []string{"conversation", "metadata", "prompt_cache_key", "custom_source_only"} {
		if containsJSONKey(got, key) {
			t.Fatalf("source-only key %q leaked into adaptor body: %s", key, converted)
		}
	}
}

func containsJSONKey(v interface{}, key string) bool {
	switch typed := v.(type) {
	case map[string]interface{}:
		if _, ok := typed[key]; ok {
			return true
		}
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}
