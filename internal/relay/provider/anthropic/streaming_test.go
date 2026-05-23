package anthropic

import (
	"strings"
	"testing"
)

func TestReverseStreamConverterMessageStopHasType(t *testing.T) {
	convert := NewReverseStreamConverter()
	_ = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":"stop"}]}` + "\n\n"))
	if strings.Contains(string(out), "message_stop") {
		t.Fatalf("finish without usage should wait for usage or DONE before message_stop, got %s", out)
	}
	out = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":12}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: message_stop") {
		t.Fatalf("missing message_stop event: %s", got)
	}
	if !strings.Contains(got, `data: {"type":"message_stop"}`) {
		t.Fatalf("message_stop data must include type discriminator: %s", got)
	}
	if !strings.Contains(got, `"stop_sequence":null`) {
		t.Fatalf("message_delta must include stop_sequence null: %s", got)
	}
}

func TestReverseStreamConverterClosesTextBeforeToolAndStopsToolsInOrder(t *testing.T) {
	convert := NewReverseStreamConverter()
	_ = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	_ = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"before"},"finish_reason":null}]}` + "\n\n"))
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"a","arguments":"{}"}},{"index":1,"id":"call_b","type":"function","function":{"name":"b","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
	got := string(out)
	textStop := strings.Index(got, `"index":0,"type":"content_block_stop"`)
	toolAStart := strings.Index(got, `"name":"a"`)
	if textStop < 0 || toolAStart < 0 || textStop > toolAStart {
		t.Fatalf("text block must close before tool block starts: %s", got)
	}

	out = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":12}}` + "\n\n"))
	got = string(out)
	stopA := strings.Index(got, `"index":1,"type":"content_block_stop"`)
	stopB := strings.Index(got, `"index":2,"type":"content_block_stop"`)
	if stopA < 0 || stopB < 0 || stopA > stopB {
		t.Fatalf("tool blocks must close in start order: %s", got)
	}
}

func TestAnthropicStreamConvertsErrorEvent(t *testing.T) {
	state := &anthropicStreamState{}
	out := state.convertLine([]byte(`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "overloaded") {
		t.Fatalf("Anthropic error event must produce downstream error chunk: %s", got)
	}
}

func TestAnthropicStreamRejectsMalformedTextDelta(t *testing.T) {
	state := &anthropicStreamState{}
	out := state.convertLine([]byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":123}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "text_delta") {
		t.Fatalf("malformed Anthropic text_delta must produce error chunk: %s", got)
	}
}

func TestAnthropicReverseStreamRejectsMalformedOpenAIJSON(t *testing.T) {
	convert := NewReverseStreamConverter()
	out := convert([]byte(`data: {bad json}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "invalid OpenAI SSE JSON") {
		t.Fatalf("malformed OpenAI SSE must produce Anthropic error event: %s", got)
	}
}

func TestAnthropicReverseStreamRejectsIncompleteToolArgumentsAtCompletion(t *testing.T) {
	convert := NewReverseStreamConverter()
	_ = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	_ = convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\""}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "valid JSON") {
		t.Fatalf("incomplete OpenAI tool arguments must produce Anthropic error event: %s", got)
	}
}

func TestAnthropicSSEBufferUsesFullEvents(t *testing.T) {
	body := []byte("event: content_block_delta\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-test\",\"usage\":{\"input_tokens\":3}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\n" +
		"data: \"text\":\"hi\"}}\n\n")
	out := string(convertAnthropicSSEBuffer(body))
	if !strings.Contains(out, `"role":"assistant"`) || !strings.Contains(out, `"content":"hi"`) {
		t.Fatalf("Anthropic buffered SSE conversion must normalize whole events: %s", out)
	}
}

func TestAnthropicStreamRequiresToolIndex(t *testing.T) {
	state := &anthropicStreamState{}
	out := state.convertLine([]byte(`data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "requires numeric index") {
		t.Fatalf("missing Anthropic tool index must produce error chunk: %s", got)
	}
}

func TestAnthropicReverseRejectsReasoningDelta(t *testing.T) {
	convert := NewReverseStreamConverter()
	out := convert([]byte(`data: {"model":"gpt-test","choices":[{"index":0,"delta":{"reasoning_content":"secret"},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "reasoning deltas") {
		t.Fatalf("reasoning delta must not become Anthropic text_delta: %s", got)
	}
}

func TestAnthropicStreamRejectsInputJSONDeltaWithoutToolStart(t *testing.T) {
	state := &anthropicStreamState{}
	out := state.convertLine([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "prior tool_use") {
		t.Fatalf("input_json_delta without prior tool start must produce error chunk: %s", got)
	}
}

func TestAnthropicReverseRejectsArgumentsBeforeFunctionName(t *testing.T) {
	convert := NewReverseStreamConverter()
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "prior function name") {
		t.Fatalf("arguments-only OpenAI tool call must produce Anthropic error event: %s", got)
	}
}
