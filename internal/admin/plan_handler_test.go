package admin

import "testing"

func TestNormalizeModelRatios(t *testing.T) {
	got, msg := normalizeModelRatios(`{"gpt-5":2,"gemini":0}`)
	if msg != "" {
		t.Fatalf("normalizeModelRatios returned error: %s", msg)
	}
	if got != `{"gemini":0,"gpt-5":2}` && got != `{"gpt-5":2,"gemini":0}` {
		t.Fatalf("normalizeModelRatios = %s", got)
	}
	if _, msg := normalizeModelRatios(`{"gpt-5":1.5}`); msg == "" {
		t.Fatal("expected integer validation error")
	}
	if _, msg := normalizeModelRatios(`{"gpt-5":-1}`); msg == "" {
		t.Fatal("expected negative ratio validation error")
	}
}
