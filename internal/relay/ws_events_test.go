package relay

import (
	"encoding/json"
	"testing"
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

func TestRelayRequestParsesGeminiMaxOutputTokens(t *testing.T) {
	var req relayRequest
	if err := json.Unmarshal([]byte(`{"model":"gemini-test","generationConfig":{"maxOutputTokens":2048}}`), &req); err != nil {
		t.Fatalf("unmarshal relay request: %v", err)
	}
	if req.GenerationConfig.MaxOutputTokens != 2048 {
		t.Fatalf("expected Gemini maxOutputTokens estimate field, got %d", req.GenerationConfig.MaxOutputTokens)
	}
}
