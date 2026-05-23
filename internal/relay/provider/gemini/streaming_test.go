package gemini

import (
	"strings"
	"testing"
)

func TestGeminiSSEBufferUsesFullEvents(t *testing.T) {
	body := []byte("event: message\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\n" +
		"data: \"hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2}}\n\n")
	out := string(convertGeminiSSEBuffer(body))
	if !strings.Contains(out, `"role":"assistant"`) || !strings.Contains(out, `"content":"hi"`) {
		t.Fatalf("Gemini buffered SSE conversion must normalize whole events: %s", out)
	}
	if !strings.Contains(out, `"prompt_tokens":1`) {
		t.Fatalf("Gemini buffered SSE conversion must preserve usage: %s", out)
	}
}

func TestGeminiStreamConvertsCodeAssistObjectWrapper(t *testing.T) {
	state := &geminiStreamState{}
	out := state.convertLine([]byte(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}}`+"\n\n"), "gemini-test")
	got := string(out)
	if !strings.Contains(got, `"content":"hi"`) {
		t.Fatalf("CodeAssist object wrapper must be converted: %s", got)
	}
}

func TestGeminiStreamRejectsMalformedTextPart(t *testing.T) {
	state := &geminiStreamState{}
	out := state.convertLine([]byte(`data: {"candidates":[{"content":{"parts":[{"text":123}]}}]}`+"\n\n"), "gemini-test")
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "text") {
		t.Fatalf("malformed Gemini stream part must produce error chunk: %s", got)
	}
}

func TestGeminiReverseStreamRejectsMalformedOpenAIJSON(t *testing.T) {
	convert := NewReverseStreamConverter()
	out := convert([]byte(`data: {bad json}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "invalid OpenAI SSE JSON") {
		t.Fatalf("malformed OpenAI SSE must produce Gemini error event: %s", got)
	}
}

func TestGeminiReverseRejectsReasoningDelta(t *testing.T) {
	convert := NewReverseStreamConverter()
	out := convert([]byte(`data: {"model":"gpt-test","choices":[{"index":0,"delta":{"reasoning_content":"secret"},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "reasoning deltas") {
		t.Fatalf("reasoning delta must not become Gemini text part: %s", got)
	}
}

func TestGeminiReverseRejectsUnnamedToolCallOnFinish(t *testing.T) {
	convert := NewReverseStreamConverter()
	_ = convert([]byte(`data: {"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n"))
	out := convert([]byte(`data: {"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "missing function name") {
		t.Fatalf("unnamed accumulated tool call must produce conversion error: %s", got)
	}
}
