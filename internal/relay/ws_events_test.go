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

func TestRelayRequestParsesGeminiMaxOutputTokens(t *testing.T) {
	var req relayRequest
	if err := json.Unmarshal([]byte(`{"model":"gemini-test","generationConfig":{"maxOutputTokens":2048}}`), &req); err != nil {
		t.Fatalf("unmarshal relay request: %v", err)
	}
	if req.GenerationConfig.MaxOutputTokens != 2048 {
		t.Fatalf("expected Gemini maxOutputTokens estimate field, got %d", req.GenerationConfig.MaxOutputTokens)
	}
}
