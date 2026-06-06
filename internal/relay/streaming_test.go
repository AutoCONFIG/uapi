package relay

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

type testUsageParser struct{}

func (testUsageParser) ParseStreamUsage([]byte) (int, int, error) {
	return 0, 0, nil
}

type errAfterReader struct {
	data []byte
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
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

func TestRetryableStreamingRequestErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "dial timeout",
			err:  errors.New("error when dialing 192.129.242.190:443: dialing to the given TCP address timed out"),
			want: true,
		},
		{
			name: "connection reset",
			err:  errors.New("connection reset by peer"),
			want: true,
		},
		{
			name: "server closed before first byte",
			err:  errors.New("the server closed connection before returning the first response byte. Make sure the server returns 'Connection: close' response header before closing the connection"),
			want: true,
		},
		{
			name: "schema error is not retryable",
			err:  errors.New("Invalid value: 'text'. Supported values are: input_text"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableStreamingRequestError(tt.err); got != tt.want {
				t.Fatalf("isRetryableStreamingRequestError() = %v, want %v", got, tt.want)
			}
		})
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
	if !result.emptyStream {
		t.Fatalf("compact data:[DONE] must be marked as empty stream: %+v", result)
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

func TestStreamAndForwardRawGeminiTerminalThenClosedNetworkCompletes(t *testing.T) {
	body := "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(&errAfterReader{
			data: []byte(body),
			err:  errors.New("read tcp 127.0.0.1:1234->127.0.0.1:443: use of closed network connection"),
		}, reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if result.err != nil || !result.finalized || result.failed {
		t.Fatalf("terminal Gemini close should complete successfully: %+v", result)
	}
}

func TestStreamAndForwardUpstreamResetAbortsDownstream(t *testing.T) {
	body := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.5\"}}\n\n" +
		"event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"Skill\"}}\n\n"
	resetErr := errors.New("read tcp 127.0.0.1:1234->127.0.0.1:443: read: connection reset by peer")

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(&errAfterReader{
			data: []byte(body),
			err:  resetErr,
		}, reader, newStreamTracker(testUsageParser{}), nil, newStreamConverterFunc(provider.FormatOpenAIResponses, provider.FormatAnthropic), false)
	}()

	_, readErr := io.ReadAll(reader)
	if readErr == nil || !strings.Contains(readErr.Error(), "connection reset by peer") {
		t.Fatalf("downstream should see upstream reset error, got %v", readErr)
	}
	result := <-done
	if result.err == nil || !strings.Contains(result.err.Error(), "connection reset by peer") {
		t.Fatalf("stream result should preserve upstream reset error: %+v", result)
	}
}

func TestStreamAndForwardRawDownstreamCloseIsClientClosed(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	go func() {
		done <- streamAndForward(pr, reader, newStreamTracker(testUsageParser{}), nil, nil, false)
	}()

	writeDone := make(chan error, 1)
	go func() {
		_, err := pw.Write([]byte("event: response.created\n" +
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.5\"}}\n\n"))
		writeDone <- err
	}()
	if err := <-writeDone; err != nil {
		t.Fatalf("write upstream event: %v", err)
	}

	buf := make([]byte, 4096)
	if n, err := reader.Read(buf); err != nil || !strings.Contains(string(buf[:n]), "response.created") {
		t.Fatalf("read first downstream event n=%d err=%v body=%s", n, err, string(buf[:n]))
	}
	_ = reader.Close()

	result := <-done
	if !errors.Is(result.err, io.ErrClosedPipe) {
		t.Fatalf("downstream close should be treated as client closed, got %+v", result)
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

func TestStreamAndForwardTracksUsageFromMultiLineResponsesEvent(t *testing.T) {
	body := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"usage\":{\"input_tokens\":17,\"output_tokens\":19,\"input_tokens_details\":{\"cached_tokens\":11}}}}\n\n"

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
	if result.promptTokens != 17 || result.completionTokens != 19 {
		t.Fatalf("usage from multiline Responses SSE event = (%d,%d), want (17,19)", result.promptTokens, result.completionTokens)
	}
	tracker := newStreamTracker(responsesUsageParser{})
	for _, payload := range sseDataPayloads([]byte(body)) {
		tracker.TrackChunk([]byte(payload))
	}
	if tracker.CacheReadTokens() != 11 {
		t.Fatalf("cache read from multiline Responses SSE event = %d, want 11", tracker.CacheReadTokens())
	}
	if !result.finalized || result.failed {
		t.Fatalf("multiline Responses terminal should finalize successfully: %+v", result)
	}
}

func TestStreamTrackerCapturesCacheReadTokens(t *testing.T) {
	tracker := newStreamTracker(responsesUsageParser{})
	tracker.TrackChunk([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":13,"input_tokens_details":{"cached_tokens":7}}}}`))

	if got := tracker.CacheReadTokens(); got != 7 {
		t.Fatalf("cache read tokens = %d, want 7", got)
	}
}

func TestStreamTrackerCapturesCacheCreationTokens(t *testing.T) {
	tracker := newStreamTracker(testUsageParser{})
	tracker.TrackChunk([]byte(`{"type":"message_delta","usage":{"output_tokens":13,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}`))

	if got := tracker.CacheCreationTokens(); got != 5 {
		t.Fatalf("cache creation tokens = %d, want 5", got)
	}
	if got := tracker.CacheReadTokens(); got != 7 {
		t.Fatalf("cache read tokens = %d, want 7", got)
	}
}

func TestStreamTrackerTotalsNestedAnthropicCacheCreationTokens(t *testing.T) {
	tracker := newStreamTracker(testUsageParser{})
	tracker.TrackChunk([]byte(`{"type":"message_delta","usage":{"output_tokens":13,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4}}}`))

	if got := tracker.CacheCreationTokens(); got != 7 {
		t.Fatalf("nested cache creation tokens = %d, want 7", got)
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

func TestStreamAndForwardConvertedDoneOnlyMarksEmptyStream(t *testing.T) {
	body := "data: [DONE]\n\n"

	reader := NewSSEStreamReader()
	done := make(chan streamResult, 1)
	outputConvert := func([]byte) []byte {
		return []byte(`event: message_delta` + "\n" +
			`data: {"delta":{"stop_reason":"end_turn"},"type":"message_delta","usage":{"output_tokens":0}}` + "\n\n" +
			`event: message_stop` + "\n" +
			`data: {"type":"message_stop"}` + "\n\n")
	}
	go func() {
		done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, outputConvert, false)
	}()

	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	result := <-done
	if !result.finalized || result.failed {
		t.Fatalf("converted DONE-only stream must still be finalized: %+v", result)
	}
	if !result.emptyStream {
		t.Fatalf("converted DONE-only stream must be marked as empty: %+v", result)
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
	if strings.Contains(got, "data: [DONE]") {
		t.Fatalf("normal Responses downstream stream must not include OpenAI Chat [DONE]:\n%s", got)
	}
}

func TestStreamAndForwardChatToResponsesDoesNotFinalizeTruncatedEOF(t *testing.T) {
	body := `data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{"}}]},"finish_reason":null}]}` + "\n\n"

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
	if result.finalized || result.failed {
		t.Fatalf("truncated chat to responses stream must not finalize successfully: %+v", result)
	}
	if strings.Contains(string(out), "response.completed") {
		t.Fatalf("truncated stream must not emit response.completed: %s", out)
	}
}

func TestStreamAndForwardNormalNonChatTargetsDoNotAppendDone(t *testing.T) {
	body := `data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	tests := []struct {
		name   string
		target provider.Format
		want   string
	}{
		{name: "responses", target: provider.FormatOpenAIResponses, want: "event: response.completed"},
		{name: "anthropic", target: provider.FormatAnthropic, want: `"type":"message_stop"`},
		{name: "gemini", target: provider.FormatGemini, want: `"finishReason":"STOP"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewSSEStreamReader()
			done := make(chan streamResult, 1)
			outputConvert := newStreamConverterFunc(provider.FormatOpenAIChatCompletions, tt.target)
			go func() {
				done <- streamAndForward(strings.NewReader(body), reader, newStreamTracker(testUsageParser{}), nil, outputConvert, false)
			}()

			out, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read stream: %v", err)
			}
			result := <-done
			if !result.finalized || result.failed {
				t.Fatalf("chat to %s stream must finalize successfully: %+v\n%s", tt.target, result, out)
			}
			got := string(out)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("converted %s stream missing %s:\n%s", tt.target, tt.want, got)
			}
			if strings.Contains(got, "data: [DONE]") {
				t.Fatalf("normal %s downstream stream must not include OpenAI Chat [DONE]:\n%s", tt.target, got)
			}
		})
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

func TestStreamConverterFuncCoversOfficialClientFormatAliases(t *testing.T) {
	tests := []struct {
		name     string
		upstream provider.Format
		client   provider.Format
		input    []byte
		want     string
	}{
		{
			name:     "codex responses stream to chat",
			upstream: provider.FormatCodexResponses,
			client:   provider.FormatOpenAIChatCompletions,
			input:    []byte(`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n"),
			want:     `"content":"hi"`,
		},
		{
			name:     "chat stream to codex responses",
			upstream: provider.FormatOpenAIChatCompletions,
			client:   provider.FormatCodexResponses,
			input:    []byte(`data: {"id":"chatcmpl-test","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n"),
			want:     "event: response.output_text.delta",
		},
		{
			name:     "claude code stream to chat",
			upstream: provider.FormatClaudeCode,
			client:   provider.FormatOpenAIChatCompletions,
			input:    []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n"),
			want:     `"content":"hi"`,
		},
		{
			name:     "chat stream to claude code",
			upstream: provider.FormatOpenAIChatCompletions,
			client:   provider.FormatClaudeCode,
			input:    []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n"),
			want:     `"type":"content_block_delta"`,
		},
		{
			name:     "gemini cli stream to chat",
			upstream: provider.FormatGeminiCLI,
			client:   provider.FormatOpenAIChatCompletions,
			input:    []byte(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n"),
			want:     `"content":"hi"`,
		},
		{
			name:     "chat stream to antigravity",
			upstream: provider.FormatOpenAIChatCompletions,
			client:   provider.FormatAntigravity,
			input:    []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n"),
			want:     `"text":"hi"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convert := newStreamConverterFunc(tt.upstream, tt.client)
			if convert == nil {
				t.Fatalf("missing converter for %s -> %s", tt.upstream, tt.client)
			}
			out := convert(tt.input)
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("converted stream missing %s:\n%s", tt.want, out)
			}
		})
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

func TestStreamToNonStreamPreservesReasoningAlias(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"reasoning":"think"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-test","model":"m","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}` + "\n\n")
	out := StreamToNonStream(body)
	got := string(out)
	for _, want := range []string{`"reasoning_content":"think"`, `"reasoning_details"`, `"text":"think"`, `"content":"answer"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream-to-non-stream lost reasoning alias %s: %s", want, got)
		}
	}
}

func TestResponsesStreamToNonStreamAggregatesNativeFunctionCall(t *testing.T) {
	body := []byte(`event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5","created_at":1780633286,"status":"in_progress"}}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":2,"item_id":"fc_1","arguments":"{\"command\":\"git status --short\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":2,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"Bash","arguments":"{\"command\":\"git status --short\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.5","status":"completed","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}

`)
	out, complete := ResponsesStreamToNonStreamChecked(body)
	got := string(out)
	if !complete {
		t.Fatalf("Responses completed event should mark stream complete: %s", got)
	}
	for _, want := range []string{`"object":"response"`, `"type":"function_call"`, `"call_id":"call_1"`, `"name":"Bash"`, `"arguments":"{\"command\":\"git status --short\"}"`, `"input_tokens":10`, `"output_tokens":2`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses native aggregation missing %s:\n%s", want, got)
		}
	}
}

func TestStreamToNonStreamForFormatKeepsResponsesFormat(t *testing.T) {
	body := []byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}` + "\n\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.5","status":"completed"}}` + "\n\n")
	out, complete, format := StreamToNonStreamForFormat(provider.FormatOpenAIResponses, body)
	got := string(out)
	if !complete || format != provider.FormatOpenAIResponses {
		t.Fatalf("Responses force-stream aggregation = complete %v format %s body %s", complete, format, got)
	}
	if !strings.Contains(got, `"object":"response"`) || strings.Contains(got, `"chat.completion"`) {
		t.Fatalf("Responses force-stream must not aggregate through Chat format: %s", got)
	}
}

func TestParseNonStreamUsageAcceptsResponsesUsage(t *testing.T) {
	prompt, completion := parseNonStreamUsage([]byte(`{"object":"response","usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}`))
	if prompt != 11 || completion != 3 {
		t.Fatalf("Responses usage parsed as prompt=%d completion=%d", prompt, completion)
	}
}

func TestParseNonStreamUsageFullAcceptsCacheUsage(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantPrompt   int
		wantComplete int
		wantCreation int
		wantRead     int
	}{
		{
			name:         "responses cached tokens detail",
			body:         `{"object":"response","usage":{"input_tokens":11,"output_tokens":3,"input_tokens_details":{"cached_tokens":7}}}`,
			wantPrompt:   11,
			wantComplete: 3,
			wantRead:     7,
		},
		{
			name:         "responses top level cache aliases",
			body:         `{"object":"response","usage":{"input_tokens":20,"output_tokens":5,"cache_creation_input_tokens":6,"cache_read_input_tokens":8}}`,
			wantPrompt:   20,
			wantComplete: 5,
			wantCreation: 6,
			wantRead:     8,
		},
		{
			name:         "anthropic nested cache creation",
			body:         `{"usage":{"input_tokens":30,"output_tokens":9,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":4},"cache_read_input_tokens":10}}`,
			wantPrompt:   30,
			wantComplete: 9,
			wantCreation: 6,
			wantRead:     10,
		},
		{
			name:         "chat completions details aliases",
			body:         `{"usage":{"prompt_tokens":40,"completion_tokens":12,"prompt_tokens_details":{"cached_write_tokens":3,"cached_read_tokens":13}}}`,
			wantPrompt:   40,
			wantComplete: 12,
			wantCreation: 3,
			wantRead:     13,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, completion, creation, read := parseNonStreamUsageFull([]byte(tt.body))
			if prompt != tt.wantPrompt || completion != tt.wantComplete || creation != tt.wantCreation || read != tt.wantRead {
				t.Fatalf("usage = prompt %d completion %d creation %d read %d, want prompt %d completion %d creation %d read %d",
					prompt, completion, creation, read, tt.wantPrompt, tt.wantComplete, tt.wantCreation, tt.wantRead)
			}
		})
	}
}

func TestRelayDebugRequestHeadersRedactsCredentials(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Api-Key", "secret")
	req.Header.Set("Content-Type", "application/json")

	headers := relayDebugRequestHeaders(req)
	if headers["Authorization"] != "[redacted]" || headers["X-Api-Key"] != "[redacted]" {
		t.Fatalf("credential headers were not redacted: %#v", headers)
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("content type missing from debug headers: %#v", headers)
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
