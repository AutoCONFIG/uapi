package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesReverseStreamResponseEventsWrapResponseObject(t *testing.T) {
	convert := NewResponsesReverseStreamConverter()
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: response.created") {
		t.Fatalf("missing response.created event: %s", got)
	}
	for _, event := range splitOpenAITestEvents(got) {
		if strings.HasPrefix(event, "event: response.created") || strings.HasPrefix(event, "event: response.in_progress") {
			payload := openAITestEventPayload(t, event)
			if payload["response"] == nil {
				t.Fatalf("response lifecycle event must wrap response object: %s", event)
			}
			if _, flattened := payload["object"]; flattened {
				t.Fatalf("response object must not be flattened in event payload: %s", event)
			}
		}
	}

	out = convert([]byte(`data: {"id":"chatcmpl-test","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\n"))
	foundCompleted := false
	for _, event := range splitOpenAITestEvents(string(out)) {
		if strings.HasPrefix(event, "event: response.completed") {
			foundCompleted = true
			payload := openAITestEventPayload(t, event)
			resp, ok := payload["response"].(map[string]interface{})
			if !ok {
				t.Fatalf("completed event missing response object: %s", event)
			}
			if resp["status"] != "completed" {
				t.Fatalf("completed response status = %#v", resp["status"])
			}
		}
	}
	if !foundCompleted {
		t.Fatalf("missing response.completed event: %s", out)
	}
}

func TestResponsesReverseStreamFunctionCallEventsUseDistinctItemAndCallIDs(t *testing.T) {
	convert := NewResponsesReverseStreamConverter()
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\""}}]},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"call_id":"call_1"`) || !strings.Contains(got, `"response_id":"resp_`) {
		t.Fatalf("function call argument event must include call_id and Responses-shaped response_id: %s", got)
	}
	if strings.Contains(got, `"response_id":"chatcmpl-test"`) {
		t.Fatalf("converted Responses stream must not reuse Chat completion id as response_id: %s", got)
	}
	if strings.Contains(got, `"item_id":"call_1"`) {
		t.Fatalf("function call argument item_id must be distinct from call_id: %s", got)
	}
}

func TestResponsesReverseStreamLengthFinishEmitsIncomplete(t *testing.T) {
	convert := NewResponsesReverseStreamConverter()
	out := convert([]byte(`data: {"id":"chatcmpl-test","model":"gpt-test","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"length"}]}` + "\n\n"))
	out = append(out, convert([]byte("data: [DONE]\n\n"))...)
	got := string(out)
	if !strings.Contains(got, "event: response.incomplete") || !strings.Contains(got, `"incomplete_details"`) {
		t.Fatalf("length finish must emit Responses incomplete event: %s", got)
	}
}

func TestResponsesToChatStreamPreservesErrorEvent(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.failed","response":{"id":"resp_test","model":"gpt-test","error":{"message":"upstream failed"}}}

`))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "upstream failed") {
		t.Fatalf("Responses failure event was not converted to downstream error: %s", got)
	}
}

func TestResponsesToChatStreamMapsIncompleteToLengthFinish(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.incomplete","response":{"id":"resp_test","model":"gpt-test","incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"finish_reason":"length"`) {
		t.Fatalf("Responses incomplete event must produce Chat length finish: %s", got)
	}
}

func TestResponsesToChatStreamMapsCreatedAtToChatCreated(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.created","response":{"id":"resp_test","model":"gpt-test","created_at":1779361234}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"created":1779361234`) {
		t.Fatalf("Responses created_at must map to Chat created in stream chunks: %s", got)
	}
}

func TestResponsesToChatStreamSkipsUnsupportedOutputItem(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning"}}` + "\n\n"))
	if out != nil {
		t.Fatalf("unsupported Responses output item should be skipped, got: %s", out)
	}
}

func TestResponsesToChatStreamFunctionCallUsesCallID(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"fc_1","call_id":"call_1","type":"function_call","name":"lookup"}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"id":"call_1"`) {
		t.Fatalf("Responses function_call must map call_id to Chat tool_call id: %s", got)
	}
	if strings.Contains(got, `"id":"fc_1"`) {
		t.Fatalf("Responses item id must not replace function call_id in Chat tool_call id: %s", got)
	}
}

func TestResponsesToChatStreamFunctionCallDeltaUsesItemIDMapping(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	_ = convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"fc_1","call_id":"call_1","type":"function_call","name":"lookup"}}` + "\n\n"))
	_ = convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"fc_2","call_id":"call_2","type":"function_call","name":"search"}}` + "\n\n"))
	out := convert([]byte(`data: {"type":"response.function_call_arguments.delta","item_id":"fc_2","delta":"{\"q\":\"hi\"}"}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"index":1`) {
		t.Fatalf("Responses item_id must map to the original Chat tool call index: %s", got)
	}
}

func TestResponsesToChatStreamAllowsMessageOutputItem(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant"}}` + "\n\n"))
	if out != nil {
		t.Fatalf("normal Responses message output item should not produce an error chunk: %s", out)
	}
}

func TestResponsesToChatStreamSkipsAnnotationsButKeepsTextDone(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"hi","annotations":[{"type":"url_citation","url":"https://example.com"}]}` + "\n\n"))
	got := string(out)
	if strings.Contains(got, `"object":"error"`) || strings.Contains(got, "annotations") {
		t.Fatalf("annotations should be skipped without error: %s", got)
	}
	if !strings.Contains(got, `"content":"hi"`) {
		t.Fatalf("text should be preserved while annotations are skipped: %s", got)
	}
}

func TestResponsesToChatStreamUsesDoneFunctionCallArgumentsWhenNoDelta(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	_ = convert([]byte(`data: {"type":"response.output_item.added","item":{"id":"fc_1","call_id":"call_1","type":"function_call","name":"lookup"}}` + "\n\n"))
	out := convert([]byte(`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"q\":\"hi\"}"}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"arguments":"{\"q\":\"hi\"}"`) {
		t.Fatalf("done arguments must be forwarded when no deltas were seen: %s", got)
	}
}

func TestResponsesToChatStreamUsesEventNameForUntypedError(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte("event: response.failed\n" +
		`data: {"response":{"id":"resp_test","model":"gpt-test","error":{"message":"failed"}}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "failed") {
		t.Fatalf("event-only response.failed must produce Chat error chunk: %s", got)
	}
}

func TestResponsesToChatStreamSkipsCompletedResponseAnnotationsButKeepsFinish(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_test","model":"gpt-test","output":[{"type":"message","content":[{"type":"output_text","text":"hi","annotations":[{"type":"url_citation"}]}]}]}}` + "\n\n"))
	got := string(out)
	if strings.Contains(got, `"object":"error"`) || strings.Contains(got, "annotations") {
		t.Fatalf("completed response annotations should be skipped without error: %s", got)
	}
	if !strings.Contains(got, `"finish_reason":"stop"`) {
		t.Fatalf("completed response finish should be preserved: %s", got)
	}
}

func splitOpenAITestEvents(body string) []string {
	parts := strings.Split(strings.TrimSpace(body), "\n\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func openAITestEventPayload(t *testing.T, event string) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("unmarshal event payload: %v", err)
			}
			return payload
		}
	}
	t.Fatalf("event has no data line: %s", event)
	return nil
}

func TestResponsesReverseStreamRejectsMalformedJSON(t *testing.T) {
	convert := NewResponsesReverseStreamConverter()
	out := convert([]byte(`data: {not-json}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: response.failed") || !strings.Contains(got, "not valid JSON") {
		t.Fatalf("malformed Chat SSE JSON must produce response.failed: %s", got)
	}
}

func TestResponsesReverseStreamRejectsReasoningDelta(t *testing.T) {
	convert := NewResponsesReverseStreamConverter()
	out := convert([]byte(`data: {"model":"gpt-test","choices":[{"index":0,"delta":{"reasoning_content":"secret"},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, "event: response.failed") || !strings.Contains(got, "reasoning deltas") {
		t.Fatalf("reasoning delta must not become visible Responses text: %s", got)
	}
}

func TestResponsesToChatStreamRejectsMalformedJSON(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	out := convert([]byte(`data: {not-json}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"object":"error"`) || !strings.Contains(got, "not valid JSON") {
		t.Fatalf("malformed Responses SSE JSON must produce Chat error chunk: %s", got)
	}
}

func TestResponsesToChatStreamUsesOutputTextDoneFallback(t *testing.T) {
	convert := NewResponsesToChatStreamConverter()
	_ = convert([]byte(`data: {"type":"response.created","response":{"id":"resp_test","model":"gpt-test"}}` + "\n\n"))
	out := convert([]byte(`data: {"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"final only","annotations":[]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"content":"final only"`) {
		t.Fatalf("output_text.done final text should be used when no delta was seen: %s", got)
	}
}
