package convert

import "testing"

func TestAnthropicToInternalAcceptsStringContent(t *testing.T) {
	body := []byte(`{"model":"claude-test","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	got, err := AnthropicToInternal(body)
	if err != nil {
		t.Fatalf("AnthropicToInternal() error = %v", err)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Content) != 1 {
		t.Fatalf("messages = %#v", got.Messages)
	}
	if got.Messages[0].Content[0].Type != "text" || got.Messages[0].Content[0].Text != "hi" {
		t.Fatalf("content = %#v", got.Messages[0].Content[0])
	}
}
