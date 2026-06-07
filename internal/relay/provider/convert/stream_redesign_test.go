package convert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/stream"
)

func TestStreamConvertersAcceptSSEAndEmitValidJSON(t *testing.T) {
	tests := []struct {
		name     string
		upstream convert.Format
		client   convert.Format
		input    string
		wantType string
	}{
		{
			name:     "responses to chat",
			upstream: convert.FormatOpenAIResponses,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"type":"response.created","id":"resp_1","model":"gpt-5"}` + "\n\n",
		},
		{
			name:     "chat to responses",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatOpenAIResponses,
			input:    `data: {"id":"chatcmpl_1","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n",
			wantType: "response.created",
		},
		{
			name:     "anthropic to chat",
			upstream: convert.FormatAnthropic,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n",
		},
		{
			name:     "chat to anthropic",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatAnthropic,
			input:    `data: {"id":"chatcmpl_1","model":"claude","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n",
			wantType: "message_start",
		},
		{
			name:     "gemini to chat",
			upstream: convert.FormatGemini,
			client:   convert.FormatOpenAIChatCompletions,
			input:    `data: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n",
		},
		{
			name:     "chat to gemini",
			upstream: convert.FormatOpenAIChatCompletions,
			client:   convert.FormatGemini,
			input:    `data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := stream.NewConverter(tt.upstream, tt.client)
			if converter == nil {
				t.Fatalf("missing stream converter for %s -> %s", tt.upstream, tt.client)
			}
			out := converter.Convert([]byte(tt.input))
			if len(out) == 0 {
				t.Fatalf("converter emitted no output")
			}
			payload := firstSSEPayload(t, out)
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				t.Fatalf("converter emitted invalid JSON: %v\n%s", err, out)
			}
			if tt.wantType != "" {
				if typ, _ := obj["type"].(string); typ != tt.wantType {
					if method, _ := obj["method"].(string); method != tt.wantType {
						t.Fatalf("unexpected event type/method: %#v", obj)
					}
				}
			}
		})
	}
}

func TestGeminiStreamIDsAreNotConstant(t *testing.T) {
	input := []byte(`data: {"method":"generateContentStream","params":{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"NOT_STARTED"}]}}` + "\n\n")
	first := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions).Convert(input)
	second := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions).Convert(input)
	firstID := jsonStringField(t, firstSSEPayload(t, first), "id")
	secondID := jsonStringField(t, firstSSEPayload(t, second), "id")
	if firstID == "" || secondID == "" {
		t.Fatalf("missing generated IDs: %q %q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("gemini stream converter generated constant ID %q", firstID)
	}
}

func TestResponsesStreamConverterHandlesStandardTextDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	start := converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	if !strings.Contains(string(start), `"id":"resp_1"`) || !strings.Contains(string(start), `"model":"gpt-5"`) {
		t.Fatalf("response metadata was not converted: %s", start)
	}
	delta := converter.Convert([]byte(`data: {"type":"response.output_text.delta","delta":"pong"}` + "\n\n"))
	if !strings.Contains(string(delta), `"content":"pong"`) {
		t.Fatalf("string text delta was not converted: %s", delta)
	}
}

func TestChatToResponsesSuppressesLeadingWhitespaceBeforeToolCall(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"))
	blank := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"content":"\n\n\n"}}]}` + "\n\n"))
	if strings.Contains(string(blank), "response.output_item.added") || strings.Contains(string(blank), "response.output_text.delta") {
		t.Fatalf("leading whitespace should not create a Responses message before tool call:\n%s", blank)
	}
	tool := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"rg oauth\"}"}}]}}]}` + "\n\n"))
	got := string(tool)
	if strings.Contains(got, "response.output_text.delta") || !strings.Contains(got, "response.output_item.added") || !strings.Contains(got, `"type":"function_call"`) {
		t.Fatalf("tool call should be emitted without blank output message:\n%s", got)
	}
}

func TestChatToResponsesFlushesLeadingWhitespaceBeforeText(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"))
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"content":"\n\n"}}]}` + "\n\n"))
	text := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n"))
	if !strings.Contains(string(text), `"delta":"\n\nhello"`) {
		t.Fatalf("leading whitespace before real text should be preserved:\n%s", text)
	}
}

func TestResponsesStreamConverterEmitsCompletedOutputText(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","output":[{"type":"message","content":[{"type":"output_text","text":"final text"}]}],"usage":{"input_tokens":3,"output_tokens":4}}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"content":"final text"`, `"finish_reason":"stop"`, `"prompt_tokens":3`, `"completion_tokens":4`, `"total_tokens":7`} {
		if !strings.Contains(got, want) {
			t.Fatalf("completed output text was not converted, missing %s:\n%s", want, got)
		}
	}
}

func TestResponsesStreamCachedTokensConvertToChatUsage(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":7}}}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"prompt_tokens":10`, `"completion_tokens":2`, `"cached_tokens":7`, `"cached_read_tokens":7`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses stream cache usage missing %s:\n%s", want, got)
		}
	}
}

func TestAnthropicStreamCacheUsageConvertsToChatUsage(t *testing.T) {
	converter := stream.NewConverter(convert.FormatAnthropic, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":20,"output_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":4},"cache_read_input_tokens":7}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"prompt_tokens":20`, `"completion_tokens":3`, `"cached_tokens":7`, `"cache_creation_input_tokens":6`, `"cached_write_tokens":6`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Anthropic stream cache usage missing %s:\n%s", want, got)
		}
	}
}

func TestGeminiStreamCachedContentConvertsToChatUsage(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions)
	out := converter.Convert([]byte(`data: {"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":5,"totalTokenCount":16,"cachedContentTokenCount":8}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"prompt_tokens":11`, `"completion_tokens":5`, `"total_tokens":16`, `"cached_tokens":8`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Gemini stream cache usage missing %s:\n%s", want, got)
		}
	}
}

func TestResponsesStreamConverterDoesNotDuplicateDoneText(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	delta := converter.Convert([]byte(`data: {"type":"response.output_text.delta","delta":"hel"}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.output_text.done","text":"hello"}` + "\n\n"))
	completed := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}` + "\n\n"))
	got := string(delta) + string(done) + string(completed)
	if strings.Count(got, `"content":"hel"`) != 1 || strings.Count(got, `"content":"lo"`) != 1 {
		t.Fatalf("done text should emit only the missing tail:\n%s", got)
	}
	if strings.Count(got, `"content":"hello"`) != 0 {
		t.Fatalf("completed text duplicated an already streamed message:\n%s", got)
	}
}

func TestChatToResponsesEmitsStandardTextDeltaSequence(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"gpt-5","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}` + "\n\n"))
	for _, want := range []string{`event: response.output_item.added`, `event: response.content_part.added`, `event: response.output_text.delta`, `"delta":"pong"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in responses stream:\n%s", want, out)
		}
	}
}

func TestChatToGeminiAccumulatesSplitToolCallArguments(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatGemini)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\""}}]},"finish_reason":null}]}` + "\n\n"))
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"weather\"}"}}]},"finish_reason":null}]}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"gemini","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"functionCall"`, `"name":"lookup"`, `"query":"weather"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("split tool-call arguments were not accumulated, missing %s:\n%s", want, got)
		}
	}
}

func TestChatToAnthropicEmitsTextBlockBeforeDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"claude","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"claude","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}` + "\n\n"))
	start := strings.Index(string(out), `"type":"content_block_start"`)
	delta := strings.Index(string(out), `"type":"content_block_delta"`)
	if start < 0 || delta < 0 || start > delta {
		t.Fatalf("anthropic stream must start text block before delta:\n%s", out)
	}
}

func TestResponsesFunctionCallDoneArgumentsConvertToAnthropicToolInput(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Agent","arguments":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"type":"tool_use"`, `"id":"call_1"`, `"name":"Agent"`, `"partial_json":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses function_call.done arguments not converted to Anthropic tool input, missing %s:\n%s", want, got)
		}
	}
}

func TestResponsesFunctionCallWithoutNameFailsBeforeInvalidAnthropicToolUse(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	out := converter.Convert([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","arguments":"{}"}}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"type":"error"`) || !strings.Contains(got, `conversion_error`) {
		t.Fatalf("missing conversion error for function_call without name:\n%s", got)
	}
	if strings.Contains(got, `"type":"tool_use"`) || strings.Contains(got, `"name":""`) {
		t.Fatalf("must not emit invalid Anthropic tool_use with empty name:\n%s", got)
	}
}

func TestResponsesFunctionCallDoneDoesNotDuplicateArgumentDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Agent"}}` + "\n\n"))
	delta := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"call_1","delta":"{\"description\":\"Audit API\""}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Agent","arguments":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"}}` + "\n\n"))
	got := string(delta) + string(done)
	if strings.Count(got, `description`) != 1 || !strings.Contains(got, `prompt`) || strings.Contains(string(done), `description`) {
		t.Fatalf("Responses function_call.done should emit only missing argument suffix:\n%s", got)
	}
}

func TestResponsesFunctionCallArgumentsDoneEmitsOnlyMissingSuffix(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Agent"}}` + "\n\n"))
	delta := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"call_1","delta":"{\"description\":\"Audit API\""}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"call_1","arguments":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"}` + "\n\n"))
	got := string(delta) + string(done)
	if strings.Count(got, `description`) != 1 || !strings.Contains(string(done), `prompt`) || strings.Contains(string(done), `description`) {
		t.Fatalf("Responses function_call_arguments.done should emit only missing suffix:\n%s", got)
	}
}

func TestResponsesFunctionCallArgumentsDoneBeforeOutputItemDoneUsesItemID(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	doneArgs := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.done","output_index":2,"item_id":"fc_1","arguments":"{\"command\":\"git status --short\"}"}` + "\n\n"))
	doneItem := converter.Convert([]byte(`data: {"type":"response.output_item.done","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Bash","arguments":"{\"command\":\"git status --short\"}"}}` + "\n\n"))
	got := string(doneArgs) + string(doneItem)
	for _, want := range []string{`"id":"call_1"`, `"name":"Bash"`, `"arguments":"{\"command\":\"git status --short\"}"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses arguments.done before output_item.done did not emit valid Chat tool_call, missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `conversion_error`) || strings.Contains(got, `"name":""`) {
		t.Fatalf("Responses item_id/call_id merge emitted invalid tool call:\n%s", got)
	}
}

func TestResponsesFunctionCallArgumentsDeltaBeforeOutputItemAddedIsBuffered(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	delta := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.delta","output_index":2,"item_id":"fc_1","delta":"{\"command\":\"git status --short\"}"}` + "\n\n"))
	added := converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Bash"}}` + "\n\n"))
	got := string(delta) + string(added)
	if string(delta) != "" {
		t.Fatalf("arguments delta before tool metadata should be buffered, got:\n%s", delta)
	}
	for _, want := range []string{`"id":"call_1"`, `"name":"Bash"`, `"arguments":"{\"command\":\"git status --short\"}"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("buffered arguments delta missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `conversion_error`) || strings.Contains(got, `"name":""`) {
		t.Fatalf("buffered arguments delta emitted invalid tool call:\n%s", got)
	}
}

func TestResponsesFunctionCallArgumentsDoneAfterAddedMayUseItemID(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":3,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"TaskList"}}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.function_call_arguments.done","output_index":3,"item_id":"fc_1","arguments":"{}"}` + "\n\n"))
	got := string(done)
	for _, want := range []string{`"type":"input_json_delta"`, `"partial_json":"{}"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses arguments.done using item_id after output_item.added missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `conversion_error`) {
		t.Fatalf("Responses arguments.done using item_id should not fail:\n%s", got)
	}
}

func TestResponsesFamilyFunctionCallDoneStopsAnthropicToolBlock(t *testing.T) {
	for _, upstream := range []convert.Format{convert.FormatOpenAIResponses, convert.FormatCodexResponses} {
		t.Run(string(upstream), func(t *testing.T) {
			converter := stream.NewConverter(upstream, convert.FormatAnthropic)
			_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}` + "\n\n"))
			added := converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":3,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"TaskList"}}` + "\n\n"))
			done := converter.Convert([]byte(`data: {"type":"response.output_item.done","output_index":3,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"TaskList","arguments":"{}"}}` + "\n\n"))
			got := string(added) + string(done)
			if !strings.Contains(got, `"type":"tool_use"`) || !strings.Contains(got, `"name":"TaskList"`) {
				t.Fatalf("Responses-family function call did not start Anthropic tool block:\n%s", got)
			}
			if !strings.Contains(got, `"type":"content_block_stop"`) {
				t.Fatalf("Responses-family function call done did not stop Anthropic tool block:\n%s", got)
			}
			if strings.Contains(got, `"type":"message_stop"`) {
				t.Fatalf("output_item.done should close the tool block but not synthesize message_stop:\n%s", got)
			}
		})
	}
}

func TestChatToolCallNameAndArgumentsInSameDeltaStartsToolFirst(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatAnthropic)
	out := converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"git status --short\"}"}}]},"finish_reason":null}]}` + "\n\n"))
	got := string(out)
	if !strings.Contains(got, `"type":"tool_use"`) || !strings.Contains(got, `"name":"Bash"`) || !strings.Contains(got, `"partial_json":"{\"command\":\"git status --short\"}"`) {
		t.Fatalf("Chat same-delta tool name+arguments should emit start then args:\n%s", got)
	}
}

func TestChatToResponsesToolCallsEmitCompleteResponsesFunctionCallEvents(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	var out []byte
	out = append(out, converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"glm-5.1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"content":"先看一下。"},"finish_reason":null}]}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec_command","arguments":""}}]},"finish_reason":null}]}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"pwd\"}"}}]},"finish_reason":null}]}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"id":"chatcmpl_1","model":"glm-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"rg oauth\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n"))...)
	got := string(out)
	for _, want := range []string{
		`event: response.output_item.added`,
		`"type":"function_call"`,
		`"call_id":"call_a"`,
		`"call_id":"call_b"`,
		`event: response.function_call_arguments.delta`,
		`"item_id":"call_a"`,
		`"delta":"{\"cmd\":\"pwd\"}"`,
		`event: response.function_call_arguments.done`,
		`event: response.output_item.done`,
		`"arguments":"{\"cmd\":\"pwd\"}"`,
		`"arguments":"{\"cmd\":\"rg oauth\"}"`,
		`event: response.completed`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Chat -> Responses tool call stream missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"call_id":""`) || strings.Contains(got, `"delta":{"arguments"`) {
		t.Fatalf("Responses function_call_arguments.delta must use item_id/output_index and string delta:\n%s", got)
	}
}

func TestResponsesCompletedFunctionCallBackfillsAnthropicToolInput(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.5","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Agent","arguments":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"}]}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"type":"tool_use"`, `"id":"call_1"`, `"name":"Agent"`, `"partial_json":"{\"description\":\"Audit API\",\"prompt\":\"Audit protocol conversion\"}"`, `"type":"message_stop"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses completed function_call not backfilled into Anthropic tool input, missing %s:\n%s", want, got)
		}
	}
}

func TestResponsesContentPartDoneBackfillsTextWithoutOutputTextDone(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"content part final"}}` + "\n\n"))
	if !strings.Contains(string(out), `"content":"content part final"`) {
		t.Fatalf("Responses content_part.done did not backfill text:\n%s", out)
	}
}

func TestStreamConvertersUseClientToProviderDirection(t *testing.T) {
	input := []byte(`data: {"id":"chatcmpl_1","model":"model","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n")
	tests := []struct {
		name   string
		client convert.Format
		want   string
	}{
		{name: "anthropic", client: convert.FormatAnthropic, want: `"type":"content_block_start"`},
		{name: "gemini", client: convert.FormatGemini, want: `"candidates"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, tt.client)
			if converter == nil {
				t.Fatalf("missing reverse converter")
			}
			out := converter.Convert(input)
			if len(out) == 0 {
				t.Fatalf("converter emitted no output")
			}
			payload := firstSSEPayload(t, out)
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				t.Fatalf("invalid JSON output: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("unexpected reverse event: %s", out)
			}
		})
	}
}

func TestGeminiStreamConverterHandlesCodeAssistResponseWrapper(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions)
	out := converter.Convert([]byte("event: message\n" +
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\n" +
		"data: \"hi\"}]}}]}}\n\n"))
	if !strings.Contains(string(out), `"content":"hi"`) {
		t.Fatalf("CodeAssist response wrapper was not converted: %s", out)
	}
}

func TestResponsesToAnthropicDoesNotRepeatCompletedTextAfterDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatAnthropic)
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"claude"}}` + "\n\n"))
	delta := converter.Convert([]byte(`data: {"type":"response.output_text.delta","output_index":0,"delta":"hello"}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.output_text.done","output_index":0,"text":"hello"}` + "\n\n"))
	completed := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"claude","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}` + "\n\n"))
	got := string(delta) + string(done) + string(completed)
	if strings.Count(got, `"text":"hello"`) != 1 {
		t.Fatalf("Anthropic stream repeated final text after delta:\n%s", got)
	}
}

func TestResponsesToGeminiDoesNotRepeatCompletedTextAfterDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatGemini)
	delta := converter.Convert([]byte(`data: {"type":"response.output_text.delta","output_index":0,"delta":"hello"}` + "\n\n"))
	done := converter.Convert([]byte(`data: {"type":"response.output_text.done","output_index":0,"text":"hello"}` + "\n\n"))
	completed := converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gemini","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}` + "\n\n"))
	got := string(delta) + string(done) + string(completed)
	if strings.Count(got, `"text":"hello"`) != 1 {
		t.Fatalf("Gemini stream repeated final text after delta:\n%s", got)
	}
}

func TestGeminiToChatFirstContentEventEmitsImmediately(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions)
	out := converter.Convert([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"h"}]},"finishReason":"NOT_STARTED"}]}` + "\n\n"))
	if !strings.Contains(string(out), `"content":"h"`) {
		t.Fatalf("Gemini first content event did not emit immediately: %s", out)
	}
}

func TestAnthropicToChatFirstContentEventEmitsImmediately(t *testing.T) {
	converter := stream.NewConverter(convert.FormatAnthropic, convert.FormatOpenAIChatCompletions)
	_ = converter.Convert([]byte(`data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"h"}}` + "\n\n"))
	if !strings.Contains(string(out), `"content":"h"`) {
		t.Fatalf("Anthropic first content event did not emit immediately: %s", out)
	}
}

func TestDirectGeminiToAnthropicStreamPreservesThoughtSignature(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatAnthropic)
	if converter == nil {
		t.Fatalf("missing IR gemini -> anthropic converter")
	}
	out := converter.Convert([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"think","thought":true,"thoughtSignature":"sig_1"}]},"finishReason":"NOT_STARTED"}]}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"type":"thinking_delta"`, `"thinking":"think"`, `"type":"signature_delta"`, `"signature":"sig_1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Gemini thought signature not preserved into Anthropic stream, missing %s:\n%s", want, got)
		}
	}
}

func TestDirectGeminiToResponsesStreamCoversTextThinkingUsageAndTerminal(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIResponses)
	if converter == nil {
		t.Fatalf("missing IR gemini -> responses converter")
	}
	out := converter.Convert([]byte(`data: {"modelVersion":"gemini-2.5-pro","candidates":[{"content":{"parts":[{"text":"think","thought":true,"thoughtSignature":"sig_1"},{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10,"cachedContentTokenCount":2}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{
		`event: response.created`,
		`event: response.reasoning_summary_text.delta`,
		`"delta":"think"`,
		`event: response.output_text.delta`,
		`"delta":"answer"`,
		`event: response.completed`,
		`"input_tokens":7`,
		`"output_tokens":3`,
		`"cached_tokens":2`,
		`"cached_read_tokens":2`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Gemini -> Responses stream missing %s:\n%s", want, got)
		}
	}
}

func TestGeminiThoughtSignatureFunctionCallPartStreamsResponsesToolCall(t *testing.T) {
	converter := stream.NewConverter(convert.FormatGeminiCode, convert.FormatOpenAIResponses)
	if converter == nil {
		t.Fatalf("missing IR gemini_code -> responses converter")
	}
	out := converter.Convert([]byte(`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"sig_1","functionCall":{"name":"exec_command","args":{"cmd":"ls -F"}}}]}}],"modelVersion":"gemini-3.1-flash-lite"}}` + "\n\n"))
	out = append(out, converter.Convert([]byte(`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10},"modelVersion":"gemini-3.1-flash-lite"}}`+"\n\n"))...)
	got := string(out)
	for _, want := range []string{
		`event: response.output_item.added`,
		`"type":"reasoning"`,
		`"encrypted_content":"sig_1"`,
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`event: response.function_call_arguments.delta`,
		`"delta":"{\"cmd\":\"ls -F\"}"`,
		`event: response.output_item.done`,
		`event: response.completed`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Gemini thoughtSignature+functionCall stream missing %s:\n%s", want, got)
		}
	}
}

func TestDirectAnthropicToGeminiStreamPreservesSignatureDelta(t *testing.T) {
	converter := stream.NewConverter(convert.FormatAnthropic, convert.FormatGemini)
	if converter == nil {
		t.Fatalf("missing IR anthropic -> gemini converter")
	}
	_ = converter.Convert([]byte(`data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude"}}` + "\n\n"))
	thinking := converter.Convert([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}` + "\n\n"))
	signature := converter.Convert([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_1"}}` + "\n\n"))
	got := string(thinking) + string(signature)
	for _, want := range []string{`"thought":true`, `"text":"think"`, `"thoughtSignature":"sig_1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Anthropic signature not preserved into Gemini stream, missing %s:\n%s", want, got)
		}
	}
}

func TestDirectAnthropicToResponsesStreamCoversTextToolThinkingAndUsage(t *testing.T) {
	converter := stream.NewConverter(convert.FormatAnthropic, convert.FormatOpenAIResponses)
	if converter == nil {
		t.Fatalf("missing IR anthropic -> responses converter")
	}
	var out []byte
	out = append(out, converter.Convert([]byte(`data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"weather\"}"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":12,"output_tokens":5,"cache_creation_input_tokens":3,"cache_read_input_tokens":4}}`+"\n\n"))...)
	got := string(out)
	for _, want := range []string{
		`event: response.created`,
		`event: response.reasoning_summary_text.delta`,
		`"delta":"think"`,
		`event: response.output_text.delta`,
		`"delta":"answer"`,
		`"type":"function_call"`,
		`"name":"lookup"`,
		`event: response.function_call_arguments.delta`,
		`"arguments":"{\"q\":\"weather\"}"`,
		`event: response.completed`,
		`"input_tokens":12`,
		`"output_tokens":5`,
		`"cached_tokens":4`,
		`"cached_write_tokens":3`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Anthropic -> Responses stream missing %s:\n%s", want, got)
		}
	}
}

func TestDirectResponsesToGeminiStreamPreservesEncryptedReasoning(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatGemini)
	if converter == nil {
		t.Fatalf("missing IR responses -> gemini converter")
	}
	_ = converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}` + "\n\n"))
	out := converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","encrypted_content":"enc_1","summary":[]}}` + "\n\n"))
	if !strings.Contains(string(out), `"thoughtSignature":"enc_1"`) {
		t.Fatalf("Responses encrypted reasoning not preserved into Gemini thoughtSignature:\n%s", out)
	}
}

func TestDirectResponsesToGeminiStreamCoversTextFunctionCallAndReasoningText(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatGemini)
	if converter == nil {
		t.Fatalf("missing IR responses -> gemini converter")
	}
	var out []byte
	out = append(out, converter.Convert([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gemini"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"plan"}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"response.output_text.delta","output_index":1,"delta":"answer"}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"response.function_call_arguments.delta","output_index":2,"item_id":"call_1","delta":{"call_id":"call_1","arguments":"{\"q\":\"weather\"}"}}`+"\n\n"))...)
	out = append(out, converter.Convert([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gemini","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"weather\"}"}]}}`+"\n\n"))...)
	got := string(out)
	for _, want := range []string{
		`"thought":true`,
		`"text":"plan"`,
		`"text":"answer"`,
		`"functionCall"`,
		`"name":"lookup"`,
		`"q":"weather"`,
		`"finishReason":"STOP"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Responses -> Gemini stream missing %s:\n%s", want, got)
		}
	}
}

func TestChatToResponsesUsesDistinctOutputIndexesForReasoningAndText(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	reasoning := converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}` + "\n\n"))
	text := converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"gpt-5","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}` + "\n\n"))
	got := string(reasoning) + string(text)
	if !strings.Contains(got, `"type":"reasoning"`) || !strings.Contains(got, `"output_index":0`) {
		t.Fatalf("reasoning item missing output_index 0:\n%s", got)
	}
	if !strings.Contains(got, `"type":"message"`) || !strings.Contains(got, `"output_index":1`) {
		t.Fatalf("message item should use output_index 1 after reasoning:\n%s", got)
	}
}

func TestChatReasoningAliasStreamsToResponsesReasoning(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	_ = converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"glm-5.1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
	got := string(converter.Convert([]byte(`data: {"id":"chatcmpl_1","created":1773896263,"model":"glm-5.1","choices":[{"index":0,"delta":{"reasoning":"think"},"finish_reason":null}]}` + "\n\n")))
	if !strings.Contains(got, `"type":"response.reasoning_summary_text.delta"`) || !strings.Contains(got, `"delta":"think"`) {
		t.Fatalf("Chat reasoning alias did not stream as Responses reasoning:\n%s", got)
	}
}

func firstSSEPayload(t *testing.T, event []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(event), "\n") {
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	t.Fatalf("missing data line in SSE event:\n%s", event)
	return ""
}

func jsonStringField(t *testing.T, payload, field string) string {
	t.Helper()
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	value, _ := obj[field].(string)
	return value
}

func TestResponsesStreamErrorConvertsToChatError(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions)
	out := converter.Convert([]byte(`data: {"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"too long"}}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`"object":"error"`, `"type":"invalid_request_error"`, `"code":"context_length_exceeded"`, `"message":"too long"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in converted error:\n%s", want, got)
		}
	}
}

func TestChatStreamErrorConvertsToResponsesFailed(t *testing.T) {
	converter := stream.NewConverter(convert.FormatOpenAIResponses, convert.FormatOpenAIResponses)
	if converter != nil {
		t.Fatalf("same-format converter should be nil")
	}
	converter = stream.NewConverter(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses)
	out := converter.Convert([]byte(`data: {"object":"error","error":{"type":"invalid_request_error","message":"bad"}}` + "\n\n"))
	got := string(out)
	for _, want := range []string{`event: response.failed`, `"type":"response.failed"`, `"message":"bad"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in converted error:\n%s", want, got)
		}
	}
}
