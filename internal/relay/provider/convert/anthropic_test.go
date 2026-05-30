package convert

import "testing"

func TestAnthropicToInternalAcceptsStringContent(t *testing.T) {
	body := []byte(`{"model":"claude-test","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	got, err := AnthropicToInternal(body)
	if err != nil {
		t.Fatalf("AnthropicToInternal() error = %v", err)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Parts) != 1 {
		t.Fatalf("messages = %#v", got.Messages)
	}
	part := got.Messages[0].Parts[0]
	if part.Kind != contentItemKindContent || part.Content.Type != "text" || part.Content.Text != "hi" {
		t.Fatalf("content part = %#v", part)
	}
}
