package relay

import "testing"

func TestEstimateMissingUsageUsesRequestAndResponseText(t *testing.T) {
	pt, ct := 0, 0
	req := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"你好，你是谁？"}]}]}`)
	resp := []byte(`{"choices":[{"message":{"role":"assistant","content":"我是一个测试助手。"}}]}`)

	estimateMissingUsage(&pt, &ct, req, resp, 0)

	if pt <= 0 {
		t.Fatalf("prompt estimate = %d, want > 0", pt)
	}
	if ct <= 0 {
		t.Fatalf("completion estimate = %d, want > 0", ct)
	}
}

func TestEstimateMissingUsagePrefersStreamCompletionEstimate(t *testing.T) {
	pt, ct := 0, 0
	req := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)

	estimateMissingUsage(&pt, &ct, req, nil, 42)

	if pt <= 0 {
		t.Fatalf("prompt estimate = %d, want > 0", pt)
	}
	if ct != 42 {
		t.Fatalf("completion estimate = %d, want stream estimate 42", ct)
	}
}

func TestEstimateStreamOutputTokensFromChatDelta(t *testing.T) {
	got := estimateStreamOutputTokens([]byte(`{"choices":[{"delta":{"content":"hello world"}}]}`))
	if got <= 0 {
		t.Fatalf("stream output estimate = %d, want > 0", got)
	}
}
