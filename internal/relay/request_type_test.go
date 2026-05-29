package relay

import (
	"encoding/json"
	"testing"
)

func TestDetectRelayRequestTypeCoversProtocolFamilies(t *testing.T) {
	cases := map[string]relayRequestType{
		"/v1/chat/completions":                  requestTypeChatCompletion,
		"/v1/responses":                         requestTypeResponses,
		"/v1/messages":                          requestTypeMessages,
		"/v1beta/models/gemini:generateContent": requestTypeGeminiGenerate,
		"/v1/images/generations":                requestTypeImageGeneration,
		"/v1/images/edits":                      requestTypeImageEdit,
		"/v1/audio/speech":                      requestTypeSpeech,
		"/v1/audio/transcriptions":              requestTypeTranscription,
		"/v1/embeddings":                        requestTypeEmbedding,
		"/v1/moderations":                       requestTypeModeration,
		"/v1/realtime/sessions":                 requestTypeRealtime,
		"/v1/videos":                            requestTypeVideoGeneration,
	}
	for path, want := range cases {
		if got := detectRelayRequestType(path); got != want {
			t.Fatalf("detectRelayRequestType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSupportsRelayRequestTypeMakesNonTextCapabilitiesExplicit(t *testing.T) {
	if !supportsRelayRequestType("antigravity", requestTypeImageGeneration) {
		t.Fatalf("antigravity should support image generation")
	}
	if !supportsRelayRequestType("antigravity", requestTypeImageEdit) {
		t.Fatalf("antigravity should support image edit via image_gen reference images")
	}
	if !supportsRelayRequestType("openai", requestTypeSpeech) {
		t.Fatalf("openai should passthrough speech")
	}
	if supportsRelayRequestType("gemini", requestTypeEmbedding) {
		t.Fatalf("gemini embeddings should remain unsupported until a converter exists")
	}
	if supportsRelayRequestType("antigravity", requestTypeRealtime) {
		t.Fatalf("antigravity realtime passthrough should remain unsupported")
	}
	if !supportsRelayRequestType("openai", requestTypeVideoGeneration) {
		t.Fatalf("openai should passthrough video generation")
	}
}

func TestNormalizeCodexResponsesRequestAddsRequiredDefaults(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","store":true,"max_output_tokens":1,"temperature":0.2,"top_k":10,"service_tier":"auto","user":"u1","tools":[{"type":"web_search_preview"}]}`))
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	if body["instructions"] != "" {
		t.Fatalf("instructions = %#v, want empty string", body["instructions"])
	}
	if body["store"] != false {
		t.Fatalf("store = %#v, want false", body["store"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %#v, want true", body["stream"])
	}
	if body["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %#v, want true", body["parallel_tool_calls"])
	}
	for _, key := range []string{"max_output_tokens", "temperature", "top_k", "service_tier", "user"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s should be removed for codex: %#v", key, body)
		}
	}
	tools, ok := body["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", body["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok || tool["type"] != "web_search" {
		t.Fatalf("tool = %#v, want web_search", tools[0])
	}
	input, ok := body["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one message item", body["input"])
	}
	first, ok := input[0].(map[string]interface{})
	if !ok || first["type"] != "message" || first["role"] != "user" {
		t.Fatalf("input[0] = %#v, want user message item", input[0])
	}
	content, ok := first["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want input_text part", first["content"])
	}
	part, ok := content[0].(map[string]interface{})
	if !ok || part["type"] != "input_text" || part["text"] != "hi" {
		t.Fatalf("content[0] = %#v, want input_text hi", content[0])
	}
}

func TestNormalizeCodexResponsesRequestConvertsSystemInputRole(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"system","content":"rules"}],"service_tier":"priority"}`))
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	first := input[0].(map[string]interface{})
	if first["role"] != "developer" {
		t.Fatalf("role = %#v, want developer", first["role"])
	}
	if body["service_tier"] != "priority" {
		t.Fatalf("priority service_tier should be preserved: %#v", body)
	}
}

func TestCleanJSONUndefinedPlaceholdersRemovesCherryStudioSentinels(t *testing.T) {
	got := cleanJSONUndefinedPlaceholders([]byte(`{"model":"gemini-2.5-pro","temperature":"[undefined]","include":["reasoning.encrypted_content","[undefined]"],"input":[{"role":"user","content":[{"type":"input_text","text":"hi","cache_control":"[undefined]"}]}],"store":false}`))
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("cleaned body is not JSON: %v", err)
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature placeholder should be removed: %s", got)
	}
	input := body["input"].([]interface{})
	first := input[0].(map[string]interface{})
	content := first["content"].([]interface{})
	part := content[0].(map[string]interface{})
	if _, ok := part["cache_control"]; ok {
		t.Fatalf("nested placeholder should be removed: %s", got)
	}
	include := body["include"].([]interface{})
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("array placeholder should be removed: %#v", include)
	}
	if body["store"] != false {
		t.Fatalf("valid false value should be preserved: %#v", body["store"])
	}
}
