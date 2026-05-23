package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

func TestResponsesToChatSkipsResponsesOnlyFields(t *testing.T) {
	ir, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"store":false,
		"include":["reasoning.encrypted_content"],
		"conversation":"conv_123",
		"reasoning":{"effort":"medium"}
	}`))
	if err != nil {
		t.Fatalf("responsesToInternal: %v", err)
	}
	out, err := internalToOpenAIChat(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIChat should skip responses-only fields: %v", err)
	}
	text := string(out)
	for _, forbidden := range []string{`"include"`, `"conversation"`, `"store"`, `"reasoning"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("responses-only field %s should not be emitted to chat: %s", forbidden, out)
		}
	}
}

func TestChatToResponsesSkipsChatOnlyFieldsAndStop(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"stop":["END"],
		"modalities":["text"],
		"prediction":{"type":"content","content":"cached"}
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses should skip chat-only fields: %v", err)
	}
	text := string(out)
	for _, forbidden := range []string{`"stop"`, `"modalities"`, `"prediction"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("chat-only field %s should not be emitted to responses: %s", forbidden, out)
		}
	}
}

func TestOpenAIChatPreservesModernStandardParams(t *testing.T) {
	body := []byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"max_completion_tokens":123,
		"modalities":["text","audio"],
		"audio":{"voice":"alloy","format":"mp3"},
		"prediction":{"type":"content","content":"cached"}
	}`)
	ir, err := openaiChatToInternal(body)
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToOpenAIChat(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIChat: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got["max_tokens"] != nil {
		t.Fatalf("max_completion_tokens input must not be downgraded to max_tokens: %s", out)
	}
	if got["max_completion_tokens"].(float64) != 123 {
		t.Fatalf("missing max_completion_tokens: %s", out)
	}
	if got["audio"] == nil || got["prediction"] == nil || got["modalities"] == nil {
		t.Fatalf("modern chat params were not preserved: %s", out)
	}
}

func TestInternalToResponsesUsesResponsesInputImageBlocks(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}
		]}]
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, `"type":"input_image"`) || !strings.Contains(text, `"image_url":"data:image/png;base64,abc"`) {
		t.Fatalf("responses input image block is not valid Responses shape: %s", out)
	}
	if strings.Contains(text, `"image_url":{"url"`) {
		t.Fatalf("responses input must not use chat-style image_url object: %s", out)
	}
}

func TestResponsesToolsRejectBuiltInSchemasForCrossFormat(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"tools":[{"type":"web_search_preview","search_context_size":"low"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "web_search_preview") {
		t.Fatalf("expected explicit built-in tool rejection, got %v", err)
	}
}

func TestOpenAIChatToolsRejectMalformedForCrossFormat(t *testing.T) {
	_, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"tools":[{"type":"web_search_preview"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "web_search_preview") {
		t.Fatalf("expected explicit non-function tool rejection, got %v", err)
	}
}

func TestOpenAIChatToolChoiceRejectsMalformedForCrossFormat(t *testing.T) {
	_, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"tool_choice":{"type":"function","function":{}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "function.name") {
		t.Fatalf("expected explicit malformed tool_choice rejection, got %v", err)
	}
}

func TestOpenAIChatContentRejectsMalformedPartsForCrossFormat(t *testing.T) {
	_, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"detail":"high"}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "image_url.url") {
		t.Fatalf("expected explicit malformed image_url rejection, got %v", err)
	}
}

func TestResponsesToolChoiceRejectsHostedChoiceForCrossFormat(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"tool_choice":{"type":"web_search_preview"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "web_search_preview") {
		t.Fatalf("expected explicit hosted tool_choice rejection, got %v", err)
	}
}

func TestResponsesRejectsUnsupportedTopLevelInputItems(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":[{"type":"reasoning","summary":[]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("expected explicit unsupported input item rejection, got %v", err)
	}
}

func TestResponsesRejectsMalformedFunctionCallInputItems(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":[{"type":"function_call","call_id":"call_1","arguments":"{}"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "name and arguments") {
		t.Fatalf("expected explicit malformed function_call rejection, got %v", err)
	}
}

func TestResponsesAllowsInputTextContentParts(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"你好"}]}]
	}`))
	if err != nil {
		t.Fatalf("input_text content part should be convertible: %v", err)
	}
}

func TestResponsesRequestPreservesStandardExtras(t *testing.T) {
	ir, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"background":true,
		"conversation":"conv_123",
		"include":["reasoning.encrypted_content"],
		"max_tool_calls":3,
		"prompt":{"id":"pmpt_123","version":"1"},
		"prompt_cache_key":"cache-key",
		"prompt_cache_retention":"24h",
		"safety_identifier":"safe-user",
		"top_logprobs":2
	}`))
	if err != nil {
		t.Fatalf("responsesToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses: %v", err)
	}
	text := string(out)
	for _, want := range []string{
		`"background":true`,
		`"conversation":"conv_123"`,
		`"include":["reasoning.encrypted_content"]`,
		`"max_tool_calls":3`,
		`"prompt":{"id":"pmpt_123","version":"1"}`,
		`"prompt_cache_key":"cache-key"`,
		`"prompt_cache_retention":"24h"`,
		`"safety_identifier":"safe-user"`,
		`"top_logprobs":2`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Responses request extra %s was not preserved: %s", want, out)
		}
	}
}

func TestInternalToResponsesResponseOmitsEmptyMessageForToolOnly(t *testing.T) {
	out, err := internalToResponsesResponse(&provider.InternalResponse{
		ID:    "resp_test",
		Model: "gpt-test",
		Choices: []provider.InternalChoice{{
			FinishReason: "tool_calls",
			Message: provider.InternalMessage{
				Role: "assistant",
				ToolCalls: []provider.InternalToolCall{{
					ID:        "call_1",
					Name:      "lookup",
					Arguments: `{"q":"hi"}`,
				}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("internalToResponsesResponse: %v", err)
	}
	text := string(out)
	if strings.Contains(text, `"type":"message"`) {
		t.Fatalf("tool-only Responses output must not include an empty message item: %s", out)
	}
	if !strings.Contains(text, `"type":"function_call"`) {
		t.Fatalf("tool call missing from Responses output: %s", out)
	}
}

func TestInternalToResponsesResponseOutputTextHasAnnotations(t *testing.T) {
	out, err := internalToResponsesResponse(&provider.InternalResponse{
		ID:    "resp_test",
		Model: "gpt-test",
		Choices: []provider.InternalChoice{{
			FinishReason: "stop",
			Message: provider.InternalMessage{
				Role:    "assistant",
				Content: []provider.InternalContentPart{{Type: "text", Text: "你好"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("internalToResponsesResponse: %v", err)
	}
	if !strings.Contains(string(out), `"annotations":[]`) {
		t.Fatalf("Responses output_text must include annotations array: %s", out)
	}
	if !strings.Contains(string(out), `"created_at":`) {
		t.Fatalf("Responses object must use created_at: %s", out)
	}
	if strings.Contains(string(out), `"created_at":0`) {
		t.Fatalf("Responses object must not use epoch created_at: %s", out)
	}
	if strings.Contains(string(out), `"created":`) {
		t.Fatalf("Responses object must not use Chat-style created field: %s", out)
	}
}

func TestInternalToResponsesResponseLengthFinishIsIncomplete(t *testing.T) {
	out, err := internalToResponsesResponse(&provider.InternalResponse{
		ID:    "resp_test",
		Model: "gpt-test",
		Choices: []provider.InternalChoice{{
			FinishReason: "length",
			Message: provider.InternalMessage{
				Role:    "assistant",
				Content: []provider.InternalContentPart{{Type: "text", Text: "partial"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("internalToResponsesResponse: %v", err)
	}
	if !strings.Contains(string(out), `"status":"incomplete"`) || !strings.Contains(string(out), `"incomplete_details"`) {
		t.Fatalf("length finish must map to Responses incomplete: %s", out)
	}
}

func TestInternalToOpenAIResponseUsesCurrentCreatedTimestamp(t *testing.T) {
	out, err := internalToOpenAIResponse(&provider.InternalResponse{
		ID:    "chatcmpl_test",
		Model: "gpt-test",
		Choices: []provider.InternalChoice{{
			Message:      provider.InternalMessage{Role: "assistant", Content: []provider.InternalContentPart{{Type: "text", Text: "hi"}}},
			FinishReason: "stop",
		}},
	})
	if err != nil {
		t.Fatalf("internalToOpenAIResponse: %v", err)
	}
	if strings.Contains(string(out), `"created":0`) {
		t.Fatalf("Chat response must not use epoch created timestamp: %s", out)
	}
}

func TestResponsesResponseRejectsUnsupportedOutputForCrossFormat(t *testing.T) {
	// reasoning output is now allowed and stored in ReasoningContent (H4 change)
	raw := []byte(`{
		"id":"resp_123",
		"object":"response",
		"created_at":1779360000,
		"status":"incomplete",
		"model":"gpt-test",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"id":"rs_1","type":"reasoning","summary":[]}],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`)
	ir, err := responsesResponseToInternal(raw)
	if err != nil {
		t.Fatalf("reasoning output should be accepted and stored in ReasoningContent, got error: %v", err)
	}
	if len(ir.Choices) == 0 {
		t.Fatal("expected choices in response")
	}
	// ReasoningContent should be populated from reasoning output item
	if len(ir.Choices[0].Message.ReasoningContent) == 0 {
		t.Logf("ReasoningContent: %v", ir.Choices[0].Message.ReasoningContent)
		// This is OK - empty summary array is valid
	}
}

func TestResponsesResponseSkipsUnsupportedOutputItem(t *testing.T) {
	ir, err := responsesResponseToInternal([]byte(`{
		"id":"resp_123",
		"model":"gpt-test",
		"output":[{"type":"reasoning","summary":[]},{"type":"file_search_call","id":"fs_1"}],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`))
	if err != nil {
		t.Fatalf("unsupported output items should be skipped without error: %v", err)
	}
	if len(ir.Choices) != 1 {
		t.Fatalf("expected one choice: %+v", ir.Choices)
	}
}

func TestResponsesIncompleteResponseMapsToChatLengthFinish(t *testing.T) {
	ir, err := responsesResponseToInternal([]byte(`{
		"id":"resp_123",
		"object":"response",
		"created_at":1779360000,
		"status":"incomplete",
		"model":"gpt-test",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial","annotations":[]}]}],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`))
	if err != nil {
		t.Fatalf("responsesResponseToInternal: %v", err)
	}
	if len(ir.Choices) != 1 || ir.Choices[0].FinishReason != "length" {
		t.Fatalf("incomplete response must map to Chat length finish: %+v", ir.Choices)
	}
}

func TestResponsesResponseSkipsAnnotationsForCrossFormat(t *testing.T) {
	raw := []byte(`{
		"id":"resp_123",
		"created_at":1779360000,
		"model":"gpt-test",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]
	}`)
	ir, err := responsesResponseToInternal(raw)
	if err != nil {
		t.Fatalf("annotations should be skipped without error: %v", err)
	}
	out, err := internalToOpenAIResponse(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIResponse: %v", err)
	}
	text := string(out)
	if strings.Contains(text, "annotations") || !strings.Contains(text, `"content":"hi"`) {
		t.Fatalf("annotations should be skipped while text is preserved: %s", out)
	}
}

func TestResponsesResponseCreatedAtMapsToChatCreated(t *testing.T) {
	ir, err := responsesResponseToInternal([]byte(`{
		"id":"resp_123",
		"created_at":1779360000,
		"model":"gpt-test",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}]
	}`))
	if err != nil {
		t.Fatalf("responsesResponseToInternal: %v", err)
	}
	out, err := internalToOpenAIResponse(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIResponse: %v", err)
	}
	if !strings.Contains(string(out), `"created":1779360000`) {
		t.Fatalf("created_at must map to chat created: %s", out)
	}
}

func TestOpenAIChatResponseRejectsRefusalForCrossFormat(t *testing.T) {
	// refusal is now stored in Refusal field and passed through (H3 change)
	raw := []byte(`{
		"id":"chatcmpl-test",
		"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":null,"refusal":"blocked"},"finish_reason":"stop"}]
	}`)
	ir, err := openaiResponseToInternal(raw)
	if err != nil {
		t.Fatalf("refusal should be accepted and stored in Refusal field, got error: %v", err)
	}
	if len(ir.Choices) == 0 || len(ir.Choices[0].Message.Content) == 0 {
		t.Fatal("expected content in choice message")
	}
	if ir.Choices[0].Message.Content[0].Refusal != "blocked" {
		t.Fatalf("expected Refusal field to contain 'blocked', got: %v", ir.Choices[0].Message.Content[0].Refusal)
	}
}

func TestOpenAIChatResponseRejectsUnsupportedContentPartForCrossFormat(t *testing.T) {
	// audio is now stored in part's Extra instead of hard error (H3 change)
	raw := []byte(`{
		"id":"chatcmpl-test",
		"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"audio","audio":{"id":"audio_1"}}]},"finish_reason":"stop"}]
	}`)
	ir, err := openaiResponseToInternal(raw)
	if err != nil {
		t.Fatalf("audio content part should be accepted, got error: %v", err)
	}
	// Audio content part is stored in the content part's Extra field
	if len(ir.Choices) == 0 || len(ir.Choices[0].Message.Content) == 0 {
		t.Fatal("expected content in choice message")
	}
	audioPart := ir.Choices[0].Message.Content[0]
	if audioPart.Type != "audio" {
		t.Fatalf("expected audio part type, got: %v", audioPart.Type)
	}
	if audioPart.Extra == nil || audioPart.Extra["audio"] == nil {
		t.Fatalf("expected audio data in part Extra, got: %v", audioPart.Extra)
	}
}

func TestOpenAIChatRejectsMalformedToolMessagesForCrossFormat(t *testing.T) {
	_, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"tool","content":"result"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "tool_call_id") {
		t.Fatalf("expected explicit malformed tool message rejection, got %v", err)
	}

	_, err = openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup"}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "function.arguments") {
		t.Fatalf("expected explicit malformed assistant tool_call rejection, got %v", err)
	}
}

func TestOpenAIChatExtrasSkippedForResponsesCrossFormat(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"response_format":{"type":"json_object"}
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses should skip chat-only fields: %v", err)
	}
	if strings.Contains(string(out), `"response_format"`) {
		t.Fatalf("chat-only response_format should not appear in responses output: %s", out)
	}
}

func TestOpenAIResponsesExtrasSkippedForChatCrossFormat(t *testing.T) {
	ir, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"include":["reasoning.encrypted_content"]
	}`))
	if err != nil {
		t.Fatalf("responsesToInternal: %v", err)
	}
	out, err := internalToOpenAIChat(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIChat should skip responses-only fields: %v", err)
	}
	if strings.Contains(string(out), `"include"`) {
		t.Fatalf("responses-only include should not appear in chat output: %s", out)
	}
}

func TestOpenAIResponsesMaxOutputTokensMapsToChatMaxCompletionTokens(t *testing.T) {
	ir, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":"你好",
		"max_output_tokens":321
	}`))
	if err != nil {
		t.Fatalf("responsesToInternal: %v", err)
	}
	out, err := internalToOpenAIChat(ir)
	if err != nil {
		t.Fatalf("internalToOpenAIChat: %v", err)
	}
	if !strings.Contains(string(out), `"max_completion_tokens":321`) {
		t.Fatalf("max_output_tokens must map to max_completion_tokens: %s", out)
	}
	if strings.Contains(string(out), `"max_tokens":`) {
		t.Fatalf("responses-to-chat must not downgrade to max_tokens: %s", out)
	}
}

func TestOpenAIChatMaxCompletionTokensMapsToResponses(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"max_completion_tokens":321
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses should accept equivalent max_completion_tokens mapping: %v", err)
	}
	if !strings.Contains(string(out), `"max_output_tokens":321`) {
		t.Fatalf("max_completion_tokens must map to max_output_tokens: %s", out)
	}
	if strings.Contains(string(out), `"max_completion_tokens":`) {
		t.Fatalf("responses output must not retain chat-only max_completion_tokens: %s", out)
	}
}

func TestOpenAIChatStopSkippedForResponsesCrossFormat(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"你好"}],
		"stop":["END"]
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses should skip stop words: %v", err)
	}
	if strings.Contains(string(out), `"stop"`) {
		t.Fatalf("stop should not appear in responses output: %s", out)
	}
}

func TestInternalStopWordsSkippedForResponsesCrossFormatFromAnySource(t *testing.T) {
	tokens := 16
	out, err := internalToResponses(&provider.InternalRequest{
		Model:     "gpt-test",
		MaxTokens: &tokens,
		StopWords: []string{"END"},
		Messages: []provider.InternalMessage{{
			Role:    "user",
			Content: []provider.InternalContentPart{{Type: "text", Text: "hi"}},
		}},
		Metadata: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("internalToResponses should skip stop words from any source: %v", err)
	}
	if strings.Contains(string(out), `"stop"`) {
		t.Fatalf("stop should not appear in responses output: %s", out)
	}
}

func TestResponsesInputRejectsAnnotationsForCrossFormat(t *testing.T) {
	_, err := responsesToInternal([]byte(`{
		"model":"gpt-test",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi","annotations":[{"type":"url_citation"}]}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "input_text") {
		t.Fatalf("expected explicit annotations rejection, got %v", err)
	}
}

func TestOpenAIChatCreatedMapsToResponsesCreatedAt(t *testing.T) {
	ir, err := openaiResponseToInternal([]byte(`{
		"id":"chatcmpl-test",
		"created":1779361234,
		"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]
	}`))
	if err != nil {
		t.Fatalf("openaiResponseToInternal: %v", err)
	}
	out, err := internalToResponsesResponse(ir)
	if err != nil {
		t.Fatalf("internalToResponsesResponse: %v", err)
	}
	if !strings.Contains(string(out), `"created_at":1779361234`) {
		t.Fatalf("chat created must map to responses created_at: %s", out)
	}
}

func TestResponsesIncompleteContentFilterMapsToChatFinishReason(t *testing.T) {
	ir, err := responsesResponseToInternal([]byte(`{
		"id":"resp_test",
		"model":"gpt-test",
		"status":"incomplete",
		"incomplete_details":{"reason":"content_filter"},
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]
	}`))
	if err != nil {
		t.Fatalf("responsesResponseToInternal: %v", err)
	}
	if got := ir.Choices[0].FinishReason; got != "content_filter" {
		t.Fatalf("expected content_filter finish reason, got %q", got)
	}
}

func TestOpenAIChatImageDetailMapsToResponsesInput(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}]}]
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses: %v", err)
	}
	if !strings.Contains(string(out), `"detail":"high"`) {
		t.Fatalf("image detail was not preserved in Responses input: %s", out)
	}
}

func TestOpenAIChatAssistantContentAndToolCallsMapToResponses(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"assistant","content":"I will call a tool.","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"uapi\"}"}}]}]
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"type":"output_text"`) || !strings.Contains(got, `I will call a tool.`) {
		t.Fatalf("assistant text was not preserved as Responses output_text: %s", got)
	}
	if !strings.Contains(got, `"type":"function_call"`) || !strings.Contains(got, `"call_id":"call_1"`) {
		t.Fatalf("assistant tool call was not preserved: %s", got)
	}
}

func TestOpenAIChatSystemMultipleTextPartsMapToInstructions(t *testing.T) {
	ir, err := openaiChatToInternal([]byte(`{
		"model":"gpt-test",
		"messages":[{"role":"system","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]},{"role":"user","content":"hi"}]
	}`))
	if err != nil {
		t.Fatalf("openaiChatToInternal: %v", err)
	}
	out, err := internalToResponses(ir)
	if err != nil {
		t.Fatalf("internalToResponses: %v", err)
	}
	if !strings.Contains(string(out), "first\\n\\nsecond") {
		t.Fatalf("system text parts were not preserved in instructions: %s", out)
	}
}
