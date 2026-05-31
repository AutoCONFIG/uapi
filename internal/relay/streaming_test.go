package relay

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

type testUsageParser struct{}

func (testUsageParser) ParseStreamUsage([]byte) (int, int, error) {
	return 0, 0, nil
}

type responsesUsageParser struct{}

func (responsesUsageParser) ParseStreamUsage(chunk []byte) (int, int, error) {
	var event struct {
		Response struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	_ = json.Unmarshal(chunk, &event)
	return event.Response.Usage.InputTokens, event.Response.Usage.OutputTokens, nil
}

func TestStreamAndForwardRawPreservesMultiLineSSEEvent(t *testing.T) {
	body := "event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.err != nil {
		t.Fatalf("streamAndForward error: %v", result.err)
	}
	if string(out) != body {
		t.Fatalf("raw SSE was not preserved\ngot:  %q\nwant: %q", string(out), body)
	}
}

func TestStreamAndForwardRawDoesNotDuplicateDone(t *testing.T) {
	body := "data: {\"choices\":[]}\n\n" +
		"data: [DONE]\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, true)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.err != nil {
		t.Fatalf("streamAndForward error: %v", result.err)
	}
	if got := strings.Count(string(out), "data: [DONE]"); got != 1 {
		t.Fatalf("DONE marker count = %d, want 1\nbody: %s", got, out)
	}
}

func TestStreamAndForwardRawAddsDoneButDoesNotFinalizeWithoutChatFinish(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, true)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.finalized || result.failed {
		t.Fatalf("raw Chat EOF without finish must not finalize successfully: %+v", result)
	}
	if !strings.Contains(string(out), "data: [DONE]") {
		t.Fatalf("missing added DONE marker: %s", out)
	}
}

func TestStreamAndForwardRawAddsDoneAndFinalizesChatFinish(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, true)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("raw Chat EOF with finish must finalize successfully: %+v", result)
	}
	if !strings.Contains(string(out), "data: [DONE]") {
		t.Fatalf("missing added DONE marker: %s", out)
	}
}

func TestStreamAndForwardRawAcceptsCompactDoneField(t *testing.T) {
	body := "data:[DONE]\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, true)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("compact data:[DONE] must finalize successfully: %+v", result)
	}
}

func TestStreamAndForwardRawGeminiEOFCompletes(t *testing.T) {
	body := "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("clean raw Gemini EOF must finalize successfully: %+v", result)
	}
}

func TestStreamAndForwardRawNonChatEOFWithoutTerminalDoesNotComplete(t *testing.T) {
	body := "event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.finalized || result.failed {
		t.Fatalf("raw non-Chat EOF without terminal must not finalize successfully: %+v", result)
	}
}

func TestStreamAndForwardRawMarksFailureTerminal(t *testing.T) {
	body := "event: response.failed\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"failed\"}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.failed {
		t.Fatalf("response.failed terminal must mark stream failed: %+v", result)
	}
}

func TestStreamAndForwardRawFinalizesOnMessageStop(t *testing.T) {
	body := "event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("message_stop terminal must finalize raw stream successfully: %+v", result)
	}
}

func TestStreamAndForwardConvertedJoinsMultiLineDataEvent(t *testing.T) {
	body := "data: {\"a\":1,\n" +
		"data: \"b\":2}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	var gotInput string
	convert := func(line []byte) []byte {
		gotInput = string(line)
		return []byte("data: {\"choices\":[]}\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), convert, nil, true)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.err != nil {
		t.Fatalf("streamAndForward error: %v", result.err)
	}
	if gotInput != "data: {\"a\":1,\n\"b\":2}\n\n" {
		t.Fatalf("converter input mismatch: %q", gotInput)
	}
}

func TestStreamAndForwardConvertedFinalizesOnResponsesCompleted(t *testing.T) {
	body := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	convert := func(line []byte) []byte {
		return []byte(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), convert, nil, true)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("response.completed must finalize converted stream successfully: %+v", result)
	}
}

func TestStreamAndForwardConvertedTracksUsageFromNamedResponsesEvent(t *testing.T) {
	body := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":13}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	convert := func(line []byte) []byte {
		return line
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(responsesUsageParser{}), convert, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.promptTokens != 11 || result.completionTokens != 13 {
		t.Fatalf("usage from named Responses SSE event = (%d,%d), want (11,13)", result.promptTokens, result.completionTokens)
	}
	if !result.finalized || result.failed {
		t.Fatalf("named Responses terminal should finalize successfully: %+v", result)
	}
}

func TestStreamTrackerCapturesCacheReadTokens(t *testing.T) {
	tracker := newStreamTracker(responsesUsageParser{})
	tracker.TrackChunk([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":13,"input_tokens_details":{"cached_tokens":7}}}}`))

	if got := tracker.CacheReadTokens(); got != 7 {
		t.Fatalf("cache read tokens = %d, want 7", got)
	}
}

func TestStreamAndForwardResponsesIncompleteIsSuccessfulTerminal(t *testing.T) {
	body := "event: response.incomplete\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("response.incomplete must finalize as a successful partial completion: %+v", result)
	}
}

func TestStreamAndForwardConvertedResponsesToChatSendsDone(t *testing.T) {
	body := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	outputConvert := func([]byte) []byte {
		return []byte(`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, outputConvert, true)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("converted Responses terminal must finalize successfully: %+v", result)
	}
	if !strings.Contains(string(out), "data: [DONE]") {
		t.Fatalf("converted Chat SSE must include [DONE], got %s", out)
	}
}

func TestStreamAndForwardChatToResponsesCompletesLifecycle(t *testing.T) {
	body := `data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	outputConvert := newStreamConverterFunc(provider.FormatOpenAIChatCompletions, provider.FormatOpenAIResponses)
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, outputConvert, false)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("chat to responses stream must finalize successfully: %+v", result)
	}
	got := string(out)
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		"event: response.output_text.done",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
		`"delta":"hi"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted Responses SSE missing %s:\n%s", want, got)
		}
	}
}

func TestStreamConverterFuncClosesOnTerminalEventWithoutDone(t *testing.T) {
	convert := newStreamConverterFunc(provider.FormatOpenAIResponses, provider.FormatOpenAIChatCompletions)
	if convert == nil {
		t.Fatalf("missing responses to chat converter")
	}
	out := convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"m"}}` + "\n\n"))
	if !strings.Contains(string(out), `"finish_reason":"stop"`) {
		t.Fatalf("terminal Responses event was not converted to Chat finish: %s", out)
	}
	if got := convert([]byte("data: [DONE]\n\n")); got != nil {
		t.Fatalf("converter should be closed after terminal event, got %s", got)
	}
}

func TestStreamConverterFuncKeepsGeminiNotStartedOpen(t *testing.T) {
	convert := newStreamConverterFunc(provider.FormatOpenAIChatCompletions, provider.FormatGemini)
	if convert == nil {
		t.Fatalf("missing chat to gemini converter")
	}
	first := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}` + "\n\n"))
	second := convert([]byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"second"},"finish_reason":null}]}` + "\n\n"))
	if !strings.Contains(string(first), `"text":"first"`) {
		t.Fatalf("first Gemini chunk missing: %s", first)
	}
	if !strings.Contains(string(second), `"text":"second"`) {
		t.Fatalf("converter closed on NOT_STARTED finishReason, second chunk: %s", second)
	}
}

func TestStreamAndForwardConvertedAnthropicMessageDeltaFinalizes(t *testing.T) {
	body := "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	outputConvert := func([]byte) []byte {
		return []byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, outputConvert, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("anthropic message_delta stop_reason must finalize converted stream: %+v", result)
	}
}

func TestStreamToNonStreamJoinsMultiLineDataEvent(t *testing.T) {
	body := []byte("data: {\"id\":\"chatcmpl-test\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你\"}\n" +
		"data: ,\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\n")
	out := StreamToNonStream(body)
	got := string(out)
	if !strings.Contains(got, "你好") {
		t.Fatalf("multi-line SSE data was not joined before non-stream conversion: %s", got)
	}
}

func TestStreamToNonStreamCheckedDetectsMissingTerminal(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n")
	_, complete := StreamToNonStreamChecked(body)
	if complete {
		t.Fatalf("stream without DONE or finish_reason must not be complete")
	}
}

func TestStreamToNonStreamPreservesContentWithToolCalls(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"I will call."},"finish_reason":null}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")
	out := StreamToNonStream(body)
	got := string(out)
	if !strings.Contains(got, `"content":"I will call."`) || !strings.Contains(got, `"tool_calls"`) {
		t.Fatalf("stream-to-non-stream must preserve assistant content with tool calls: %s", got)
	}
}

func TestStreamToNonStreamPreservesLengthFinishWithToolCalls(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"length"}]}` + "\n\n")
	out := StreamToNonStream(body)
	got := string(out)
	if !strings.Contains(got, `"finish_reason":"length"`) {
		t.Fatalf("stream-to-non-stream must preserve length finish with tool calls: %s", got)
	}
}

func TestStreamToNonStreamPreservesReasoningDetails(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think","reasoning_details":[{"index":0,"type":"reasoning.text","text":"think"}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"reasoning_details":[{"index":1,"type":"reasoning.encrypted","data":"enc_1","encrypted_content":"enc_1"}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}` + "\n\n")
	out := StreamToNonStream(body)
	got := string(out)
	for _, want := range []string{`"reasoning_content":"think"`, `"reasoning_details"`, `"data":"enc_1"`, `"content":"answer"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream-to-non-stream lost reasoning detail %s: %s", want, got)
		}
	}
}

func TestStreamAndForwardConvertedPreservesEventNameForConverter(t *testing.T) {
	body := "event: response.failed\n" +
		"data: {\"response\":{\"error\":{\"message\":\"failed\"}}}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	converter := func(event []byte) []byte {
		if !strings.Contains(string(event), "event: response.failed") {
			return nil
		}
		return []byte("data: {\"object\":\"error\"}\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), converter, nil, false)
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.err != nil {
		t.Fatalf("streamAndForward error: %v", result.err)
	}
	if !result.failed {
		t.Fatalf("response.failed event must mark stream failed: %+v", result)
	}
	if !strings.Contains(string(out), `"object":"error"`) {
		t.Fatalf("converter did not receive event name / output missing: %s", out)
	}
}

func TestNormalizeSSEEventPreservesSignificantDataWhitespace(t *testing.T) {
	event := []byte("event: response.output_text.delta\n" +
		"data: {\"delta\":\" leading and trailing \"}\n\n")
	got := string(normalizeSSEEventForConverterWithEvent(event))
	if !strings.Contains(got, `" leading and trailing "`) {
		t.Fatalf("significant data whitespace was not preserved: %q", got)
	}
}
