package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
)

func TestResponsesWSIncompleteIsSuccessfulTerminal(t *testing.T) {
	if !IsTerminalEvent(WSEventResponseIncomp) {
		t.Fatalf("response.incomplete must remain terminal")
	}
	if !IsSuccessfulTerminalEvent(WSEventResponseIncomp) {
		t.Fatalf("response.incomplete must settle as a successful partial completion")
	}
	if IsFailureTerminalEvent(WSEventResponseIncomp) {
		t.Fatalf("response.incomplete must not be treated as a failure terminal")
	}
}

func TestEstimateTokensFromCreateEventUsesMaxOutputTokens(t *testing.T) {
	got := EstimateTokensFromCreateEvent([]byte(`{"type":"response.create","model":"gpt-test","max_output_tokens":4096}`))
	if got != 4096 {
		t.Fatalf("expected max_output_tokens estimate, got %d", got)
	}
}

func TestWSUsageEstimateFallbackUsesCreateAndCompletedText(t *testing.T) {
	pt, ct := 0, 0
	create := wsCreateToHTTPBody([]byte(`{"type":"response.create","model":"gpt-test","input":[{"role":"user","content":[{"type":"input_text","text":"你好"}]}]}`))
	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"你好，我是测试助手。"}]}]}}`)

	estimateMissingUsage(&pt, &ct, create, completed, 0)

	if pt <= 0 || ct <= 0 {
		t.Fatalf("ws usage fallback = (%d,%d), want both > 0", pt, ct)
	}
}

func TestWSTerminalUsageExtractsProviderCacheTokens(t *testing.T) {
	completed := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":13,"input_tokens_details":{"cached_tokens":7},"cache_creation_input_tokens":5}}}`)

	pt, ct := ParseResponsesUsage(completed)
	if pt != 11 || ct != 13 {
		t.Fatalf("responses usage = (%d,%d), want (11,13)", pt, ct)
	}
	if got := extractStreamCacheReadTokens(completed); got != 7 {
		t.Fatalf("cache read tokens = %d, want 7", got)
	}
	if got := extractStreamCacheCreationTokens(completed); got != 5 {
		t.Fatalf("cache creation tokens = %d, want 5", got)
	}
}

func TestWSCreateToHTTPBodyCleansUndefinedPlaceholders(t *testing.T) {
	body := wsCreateToHTTPBody([]byte(`{"type":"response.create","model":"gpt-test","input":[{"role":"user","content":[{"type":"input_text","text":"hi","cache_control":"[undefined]"}]}],"metadata":{"bad":"[undefined]"}}`))
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("ws http body is not JSON: %v", err)
	}
	if metadata, _ := decoded["metadata"].(map[string]interface{}); len(metadata) != 0 {
		t.Fatalf("metadata placeholder should be cleaned: %#v", metadata)
	}
	input := decoded["input"].([]interface{})
	content := input[0].(map[string]interface{})["content"].([]interface{})
	part := content[0].(map[string]interface{})
	if _, ok := part["cache_control"]; ok {
		t.Fatalf("nested input placeholder should be cleaned: %s", body)
	}
}

func TestWSCreateToHTTPBodyPreservesResponsesFields(t *testing.T) {
	body := wsCreateToHTTPBody([]byte(`{"type":"response.create","event_id":"evt_1","model":"gpt-test","input":[],"parallel_tool_calls":false,"max_tool_calls":3,"tool_choice":"auto","stream":false}`))
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("ws http body is not JSON: %v", err)
	}
	if _, ok := decoded["type"]; ok {
		t.Fatalf("type should be stripped from HTTP body: %s", body)
	}
	if _, ok := decoded["event_id"]; ok {
		t.Fatalf("event_id should be stripped from HTTP body: %s", body)
	}
	if decoded["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls not preserved: %#v", decoded["parallel_tool_calls"])
	}
	if decoded["max_tool_calls"] == nil {
		t.Fatalf("max_tool_calls not preserved: %s", body)
	}
	if decoded["stream"] != true {
		t.Fatalf("stream should be forced true: %#v", decoded["stream"])
	}
}

func TestWSCreateToNativeMessageDropsHTTPOnlyFields(t *testing.T) {
	body := wsCreateToNativeMessage([]byte(`{"type":"response.create","event_id":"evt_1","model":"gpt-test","input":"hi","stream":true,"background":true,"parallel_tool_calls":false}`))
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("native ws body is not JSON: %v", err)
	}
	if decoded["type"] != WSEventResponseCreate {
		t.Fatalf("type = %#v, want response.create", decoded["type"])
	}
	if decoded["model"] != "gpt-test" || decoded["input"] != "hi" {
		t.Fatalf("payload fields not preserved: %s", body)
	}
	if decoded["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls not preserved: %#v", decoded["parallel_tool_calls"])
	}
	if _, ok := decoded["stream"]; ok {
		t.Fatalf("stream should be stripped for native WS: %s", body)
	}
	if _, ok := decoded["background"]; ok {
		t.Fatalf("background should be stripped for native WS: %s", body)
	}
}

func TestWSCreateToNativeMessageFlattensNestedResponse(t *testing.T) {
	body := wsCreateToNativeMessage([]byte(`{"type":"response.create","event_id":"evt_1","response":{"model":"gpt-test","input":"hi","stream":true}}`))
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("native ws body is not JSON: %v", err)
	}
	if decoded["type"] != WSEventResponseCreate || decoded["event_id"] != "evt_1" {
		t.Fatalf("native envelope fields not normalized: %s", body)
	}
	if decoded["model"] != "gpt-test" || decoded["input"] != "hi" {
		t.Fatalf("nested response not flattened: %s", body)
	}
	if _, ok := decoded["response"]; ok {
		t.Fatalf("response wrapper should be stripped: %s", body)
	}
	if _, ok := decoded["stream"]; ok {
		t.Fatalf("stream should be stripped for native WS: %s", body)
	}
}

func TestWSHTTPBridgeAppliesCachePolicy(t *testing.T) {
	body, err := convertWSHTTPBridgeRequestBody(
		&db.Channel{Type: "openai", APIFormat: "responses"},
		nil,
		nil,
		[]byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stable","cache_control":{"type":"ephemeral"}}]}]}`),
	)
	if err != nil {
		t.Fatalf("convertWSHTTPBridgeRequestBody: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"prompt_cache_key":"uapi-cache-`) {
		t.Fatalf("bridge body missing synthesized prompt_cache_key: %s", body)
	}
	if strings.Contains(text, `"cache_control"`) {
		t.Fatalf("Responses bridge body should not forward content cache_control: %s", body)
	}
}

func TestWSHTTPBridgeNormalizesCodexRequestShape(t *testing.T) {
	body, err := convertWSHTTPBridgeRequestBody(
		&db.Channel{Base: db.Base{ID: mustUUID(t, "11111111-1111-1111-1111-111111111111")}, Type: "openai", APIFormat: "codex"},
		&db.Account{Base: db.Base{ID: mustUUID(t, "22222222-2222-2222-2222-222222222222")}},
		nil,
		[]byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"reasoning":{"effort":"high"}}`),
	)
	if err != nil {
		t.Fatalf("convertWSHTTPBridgeRequestBody: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"prompt_cache_key":`,
		`"client_metadata":`,
		`"x-codex-installation-id":`,
		`"include":["reasoning.encrypted_content"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Codex bridge body missing %s: %s", want, body)
		}
	}
}

func TestWSToolCallRepairPrependsCachedFunctionCallOutput(t *testing.T) {
	cache := newWSToolCallCache(0, 0)
	cache.RecordResponseEvent("sess-1", []byte(`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"a.go\"}","status":"completed"}}`))

	repaired := cache.RepairCreate("sess-1", []byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`))
	var decoded struct {
		Input []map[string]interface{} `json:"input"`
	}
	if err := json.Unmarshal(repaired, &decoded); err != nil {
		t.Fatalf("repaired create is not JSON: %v", err)
	}
	if len(decoded.Input) != 2 {
		t.Fatalf("input len = %d, want 2; body=%s", len(decoded.Input), repaired)
	}
	if decoded.Input[0]["type"] != "function_call" || decoded.Input[0]["call_id"] != "call_1" {
		t.Fatalf("missing cached function_call before output: %s", repaired)
	}
	if decoded.Input[1]["type"] != "function_call_output" {
		t.Fatalf("output moved unexpectedly: %s", repaired)
	}
}

func TestWSToolCallRepairDoesNotDuplicateExistingCall(t *testing.T) {
	cache := newWSToolCallCache(0, 0)
	cache.RecordResponseEvent("sess-1", []byte(`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"}}`))

	payload := []byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	repaired := cache.RepairCreate("sess-1", payload)
	var decoded struct {
		Input []map[string]interface{} `json:"input"`
	}
	if err := json.Unmarshal(repaired, &decoded); err != nil {
		t.Fatalf("repaired create is not JSON: %v", err)
	}
	if len(decoded.Input) != 2 {
		t.Fatalf("input len = %d, want 2; body=%s", len(decoded.Input), repaired)
	}
}

func TestWSToolCallRepairSupportsCustomToolCalls(t *testing.T) {
	cache := newWSToolCallCache(0, 0)
	cache.RecordCreate("sess-1", []byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"custom_tool_call","call_id":"call_custom","name":"apply_patch","input":"patch"}]}`))

	repaired := cache.RepairCreate("sess-1", []byte(`{"type":"response.create","model":"gpt-test","input":[{"type":"custom_tool_call_output","call_id":"call_custom","output":"ok"}]}`))
	var decoded struct {
		Input []map[string]interface{} `json:"input"`
	}
	if err := json.Unmarshal(repaired, &decoded); err != nil {
		t.Fatalf("repaired create is not JSON: %v", err)
	}
	if len(decoded.Input) != 2 {
		t.Fatalf("input len = %d, want 2; body=%s", len(decoded.Input), repaired)
	}
	if decoded.Input[0]["type"] != "custom_tool_call" {
		t.Fatalf("missing cached custom_tool_call before output: %s", repaired)
	}
}

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRelayRequestParsesGeminiMaxOutputTokens(t *testing.T) {
	var req relayRequest
	if err := json.Unmarshal([]byte(`{"model":"gemini-test","generationConfig":{"maxOutputTokens":2048}}`), &req); err != nil {
		t.Fatalf("unmarshal relay request: %v", err)
	}
	if req.GenerationConfig.MaxOutputTokens != 2048 {
		t.Fatalf("expected Gemini maxOutputTokens estimate field, got %d", req.GenerationConfig.MaxOutputTokens)
	}
}
