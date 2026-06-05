package convert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func TestChatToResponsesAlwaysEmitsInstructions(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	value, ok := got["instructions"]
	if !ok {
		t.Fatalf("instructions field missing in Responses body: %s", converted)
	}
	if value != "" {
		t.Fatalf("instructions = %#v, want empty string", value)
	}
}

func TestCrossProtocolRequestConversions(t *testing.T) {
	chat := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}],"temperature":0.2}`)
	tests := []struct {
		name   string
		target convert.Format
		want   []string
	}{
		{name: "chat to anthropic", target: convert.FormatAnthropic, want: []string{`"system"`, `"messages"`, `"max_tokens"`}},
		{name: "chat to gemini", target: convert.FormatGemini, want: []string{`"systemInstruction"`, `"contents"`}},
		{name: "chat to responses", target: convert.FormatOpenAIResponses, want: []string{`"instructions":"be brief"`, `"input"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, tt.target, chat)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			if !json.Valid(converted) {
				t.Fatalf("converted body is not valid JSON: %s", converted)
			}
			text := string(converted)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("converted body missing %s: %s", want, text)
				}
			}
		})
	}
}

func TestChatToResponsesPreservesRichContentAndToolCalls(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"look"},
				{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}
			]},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"uapi\"}"}}]}
		]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	input := got["input"].([]interface{})
	message := findObjectByType(input, "message")
	parts := message["content"].([]interface{})
	text := parts[0].(map[string]interface{})
	if text["type"] != "input_text" || text["text"] != "look" {
		t.Fatalf("text part not converted to Responses input_text: %s", converted)
	}
	image := parts[1].(map[string]interface{})
	if image["type"] != "input_image" || image["image_url"] != "https://example.com/a.png" || image["detail"] != "high" {
		t.Fatalf("image URL detail not preserved for Responses: %s", converted)
	}
	call := findObjectByType(input, "function_call")
	if call["call_id"] != "call_1" || call["name"] != "lookup" || call["arguments"] == "" {
		t.Fatalf("assistant tool call not emitted as Responses function_call: %s", converted)
	}
}

func TestOpenAIResponsesSameFormatPreservesRichInputItems(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"id":"msg_1","type":"message","role":"user","status":"completed","content":[
				{"type":"input_text","text":"read this","cache_control":{"type":"ephemeral"}},
				{"type":"input_file","file_data":"Zm9v","filename":"note.txt","file_type":"text/plain"},
				{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}
			]},
			{"id":"fs_1","type":"file_search_call","status":"completed","queries":["uapi"],"results":[]}
		],
		"include":["reasoning.encrypted_content"],
		"previous_response_id":"resp_1",
		"metadata":{"trace":"abc"},
		"parallel_tool_calls":false,
		"store":false
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"cache_control"`,
		`"file_data":"Zm9v"`,
		`"input_audio"`,
		`"type":"file_search_call"`,
		`"include":["reasoning.encrypted_content"]`,
		`"previous_response_id":"resp_1"`,
		`"metadata":{"trace":"abc"}`,
		`"parallel_tool_calls":false`,
		`"store":false`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-format Responses conversion dropped %s:\n%s", want, converted)
		}
	}
}

func TestOpenAIChatSameFormatPreservesExplicitFalseAndNativeFields(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"user","content":""},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}]}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true},"cache_control":{"type":"ephemeral"}}],
		"max_completion_tokens":123,
		"logprobs":false,
		"parallel_tool_calls":false,
		"store":false,
		"stream_options":{"include_usage":true}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"content":[{"type":"text","text":""}]`,
		`"max_completion_tokens":123`,
		`"logprobs":false`,
		`"parallel_tool_calls":false`,
		`"store":false`,
		`"stream_options":{"include_usage":true}`,
		`"strict":true`,
		`"cache_control":{"type":"ephemeral"}`,
		`9007199254740993`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-format Chat conversion dropped %s:\n%s", want, converted)
		}
	}
	if strings.Contains(string(converted), `"max_tokens":123`) {
		t.Fatalf("max_completion_tokens was rewritten as max_tokens:\n%s", converted)
	}
}

func TestOpenAIChatSameProtocolNormalizePreservesUnknownTopLevel(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"metadata":{"trace":"abc"},
		"prediction":{"type":"content","content":"draft"},
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true}}],
		"reasoning":"[undefined]",
		"temperature":"[undefined]"
	}`)
	normalized, err := convert.NormalizeRequestSameProtocol(convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("NormalizeRequestSameProtocol: %v", err)
	}
	got := string(normalized)
	for _, want := range []string{
		`"metadata":{"trace":"abc"}`,
		`"prediction":`,
		`"type":"content"`,
		`"content":"draft"`,
		`"strict":true`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("same-protocol normalize dropped %s:\n%s", want, normalized)
		}
	}
	if strings.Contains(got, "[undefined]") || strings.Contains(got, `"temperature"`) || strings.Contains(got, `"reasoning"`) {
		t.Fatalf("undefined placeholders were not cleaned precisely:\n%s", normalized)
	}
}

func TestOpenAIChatLogitBiasSurvivesIRConversionAndRecordsTargetLoss(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"logit_bias":{"123":-100}
	}`)
	reqIR, err := convert.ToIR(convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ToIR: %v", err)
	}
	roundTrip, err := convert.FromIR(reqIR, convert.FormatOpenAIChatCompletions)
	if err != nil {
		t.Fatalf("FromIR chat: %v", err)
	}
	if !strings.Contains(string(roundTrip), `"logit_bias":{"123":-100}`) {
		t.Fatalf("IR conversion dropped logit_bias:\n%s", roundTrip)
	}

	_, detailedIR, err := convert.ConvertRequestDetailed(convert.FormatOpenAIChatCompletions, convert.FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertRequestDetailed: %v", err)
	}
	if detailedIR.Generation.LogitBias == nil || !strings.Contains(string(detailedIR.Generation.LogitBias), `"123":-100`) {
		t.Fatalf("detailed IR did not preserve logit_bias: %#v", detailedIR.Generation.LogitBias)
	}
	foundLoss := false
	for _, loss := range detailedIR.Losses {
		if loss.Field == "logit_bias" && loss.Preserved {
			foundLoss = true
			break
		}
	}
	if !foundLoss {
		t.Fatalf("missing preserved logit_bias loss record: %#v", detailedIR.Losses)
	}
}

func TestOpenAIChatToolExtensionsSurviveCrossProtocolConversion(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true},"cache_control":{"type":"ephemeral"}}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"strict":true`,
		`"cache_control":{"type":"ephemeral"}`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("tool extension dropped %s:\n%s", want, converted)
		}
	}
}

func TestProviderBridgePreservesNativeMessageAndToolPrecision(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"id":"msg_1","type":"message","role":"user","status":"completed","content":[
				{"type":"input_text","text":"read this","cache_control":{"type":"ephemeral"}}
			]},
			{"id":"fs_1","type":"file_search_call","status":"completed","queries":["uapi"],"score":9007199254740993}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true},"cache_control":{"type":"ephemeral"}}],
		"include":["reasoning.encrypted_content"],
		"parallel_tool_calls":false
	}`)
	reqIR, err := convert.ToIR(convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ToIR: %v", err)
	}
	converted, err := convert.FromIR(reqIR, convert.FormatOpenAIResponses)
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	for _, want := range []string{
		`"id":"msg_1"`,
		`"status":"completed"`,
		`"type":"file_search_call"`,
		`9007199254740993`,
		`"strict":true`,
		`"cache_control":{"type":"ephemeral"}`,
		`"parallel_tool_calls":false`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("provider conversion dropped %s:\n%s", want, converted)
		}
	}
}

func TestGeminiSameFormatPreservesFileAndCodeParts(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[
			{"fileData":{"mimeType":"application/pdf","fileUri":"files/report.pdf"}},
			{"executableCode":{"language":"PYTHON","code":"print(1)"}},
			{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1\n"}}
		]}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"fileData":{"fileUri":"files/report.pdf","mimeType":"application/pdf"}`,
		`"executableCode":{"code":"print(1)","language":"PYTHON"}`,
		`"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1\n"}`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-format Gemini conversion dropped %s:\n%s", want, converted)
		}
	}
}

func TestSameFormatGeminiPreservesUnmodeledCodeAssistFields(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"cachedContent":"cachedContents/abc",
		"labels":{"source":"uapi"},
		"generationConfig":{
			"responseLogprobs":true,
			"logprobs":3,
			"presencePenalty":0.1,
			"frequencyPenalty":0.2,
			"seed":42,
			"routingConfig":{"autoMode":{}},
			"responseModalities":["TEXT"],
			"mediaResolution":"MEDIA_RESOLUTION_LOW",
			"audioTimestamp":true
		}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"cachedContent":"cachedContents/abc"`,
		`"labels":{"source":"uapi"}`,
		`"responseLogprobs":true`,
		`"presencePenalty":0.1`,
		`"routingConfig":{"autoMode":{}}`,
		`"responseModalities":["TEXT"]`,
		`"audioTimestamp":true`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-format Gemini conversion dropped %s:\n%s", want, converted)
		}
	}
}

func TestGeminiCachedContentMapsThroughIR(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"cachedContent":"cachedContents/abc"
	}`)
	reqIR, err := convert.ToIR(convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ToIR: %v", err)
	}
	if reqIR.Cache.CachedContent != "cachedContents/abc" {
		t.Fatalf("Cache.CachedContent = %q, want cachedContents/abc", reqIR.Cache.CachedContent)
	}
	converted, err := convert.FromIR(reqIR, convert.FormatGemini)
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	if !strings.Contains(string(converted), `"cachedContent":"cachedContents/abc"`) {
		t.Fatalf("Gemini cachedContent not emitted from IR:\n%s", converted)
	}
}

func TestOpenAIChatEmitterRejectsMissingToolIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		req  *relayir.Request
		want string
	}{
		{
			name: "missing tool call function name",
			req: &relayir.Request{
				Model: "gpt-5",
				Turns: []relayir.Turn{{
					Role: relayir.RoleAssistant,
					Items: []relayir.Item{{
						Kind:          relayir.ItemToolUse,
						OriginalIndex: 7,
						CallID:        "call_1",
					}},
				}},
			},
			want: "cannot emit OpenAI Chat tool_call for IR item 7: missing required function name",
		},
		{
			name: "missing tool call id",
			req: &relayir.Request{
				Model: "gpt-5",
				Turns: []relayir.Turn{{
					Role: relayir.RoleAssistant,
					Items: []relayir.Item{{
						Kind:          relayir.ItemToolUse,
						OriginalIndex: 8,
						Name:          "lookup",
					}},
				}},
			},
			want: "cannot emit OpenAI Chat tool_call for IR item 8: missing required id",
		},
		{
			name: "missing tool result call id",
			req: &relayir.Request{
				Model: "gpt-5",
				Turns: []relayir.Turn{{
					Role: relayir.RoleTool,
					Items: []relayir.Item{{
						Kind:          relayir.ItemToolResult,
						OriginalIndex: 9,
					}},
				}},
			},
			want: "cannot emit OpenAI Chat tool result for IR item 9: missing required tool_call_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convert.FromIR(tt.req, convert.FormatOpenAIChatCompletions)
			if err == nil {
				t.Fatalf("FromIR returned nil error, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestCoreEmittersRejectToolResultWithoutCallID(t *testing.T) {
	req := &relayir.Request{
		Model: "model-test",
		Turns: []relayir.Turn{{
			Role: relayir.RoleTool,
			Items: []relayir.Item{{
				Kind:          relayir.ItemToolResult,
				OriginalIndex: 19,
				ToolResult: &relayir.ToolResult{
					OutputText: "ok",
				},
			}},
		}},
	}
	tests := []struct {
		name   string
		format convert.Format
		want   string
	}{
		{name: "chat", format: convert.FormatOpenAIChatCompletions, want: "missing required tool_call_id"},
		{name: "responses", format: convert.FormatOpenAIResponses, want: "missing required call_id"},
		{name: "anthropic", format: convert.FormatAnthropic, want: "missing required tool_use_id"},
		{name: "gemini", format: convert.FormatGemini, want: "missing required name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convert.FromIR(req, tt.format)
			if err == nil {
				t.Fatalf("FromIR returned nil error, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestAnthropicSameFormatPreservesSystemAndOrderedBlocks(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":1024,
		"system":[{"type":"text","text":"cache me","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"assistant","content":[
			{"type":"thinking","thinking":"plan","signature":"sig"},
			{"type":"text","text":"answer","cache_control":{"type":"ephemeral"}},
			{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"uapi"}}
		]}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatAnthropic, convert.FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	for _, want := range []string{
		`"system":[{`,
		`"text":"cache me"`,
		`"thinking":"plan"`,
		`"signature":"sig"`,
		`"cache_control":{"type":"ephemeral"}`,
		`"type":"tool_use"`,
		`"input":{"q":"uapi"}`,
	} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-format Anthropic conversion dropped %s:\n%s", want, converted)
		}
	}
	if strings.Index(string(converted), `"type":"thinking"`) > strings.Index(string(converted), `"type":"text"`) {
		t.Fatalf("Anthropic block order changed:\n%s", converted)
	}
}

func TestSameProtocolNormalizerRemovesUndefinedSentinels(t *testing.T) {
	tests := []struct {
		name   string
		format convert.Format
		body   []byte
	}{
		{
			name:   "responses",
			format: convert.FormatOpenAIResponses,
			body: []byte(`{
				"model":"gpt-5.5",
				"input":[{"role":"user","content":[{"type":"input_text","text":"hi","cache_control":"[undefined]"}]}],
				"temperature":"[undefined]",
				"tools":"[undefined]",
				"stream":true
			}`),
		},
		{
			name:   "gemini",
			format: convert.FormatGemini,
			body: []byte(`{
				"contents":[{"role":"user","parts":[{"text":"hi"}]}],
				"generationConfig":{"maxOutputTokens":"[undefined]","temperature":"[undefined]"},
				"systemInstruction":"[undefined]",
				"tools":"[undefined]"
			}`),
		},
		{
			name:   "anthropic",
			format: convert.FormatAnthropic,
			body: []byte(`{
				"model":"claude-test",
				"max_tokens":4096,
				"system":"[undefined]",
				"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":"[undefined]"}]}],
				"tools":"[undefined]",
				"temperature":"[undefined]"
			}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := convert.NormalizeRequestSameProtocol(tt.format, tt.body)
			if err != nil {
				t.Fatalf("NormalizeRequestSameProtocol: %v", err)
			}
			if !json.Valid(normalized) {
				t.Fatalf("normalized body is not JSON: %s", normalized)
			}
			if strings.Contains(string(normalized), "[undefined]") {
				t.Fatalf("undefined sentinel survived same-protocol normalization:\n%s", normalized)
			}
		})
	}
}

func TestSameProtocolNormalizePreservesNativeGeminiStreamBodyShape(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hi","vendorField":{"keep":true}}]}],
		"generationConfig":{"maxOutputTokens":"[undefined]","responseModalities":["TEXT"]},
		"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":{"type":"object"}}]}]
	}`)
	normalized, err := convert.NormalizeRequestSameProtocol(convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("NormalizeRequestSameProtocol: %v", err)
	}
	got := string(normalized)
	for _, want := range []string{`"contents"`, `"vendorField"`, `"responseModalities"`, `"tools"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("same-protocol normalize dropped native field %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"stream"`) {
		t.Fatalf("same-protocol Gemini normalize must not inject OpenAI stream field:\n%s", got)
	}
	if strings.Contains(got, "[undefined]") || strings.Contains(got, "maxOutputTokens") {
		t.Fatalf("undefined generationConfig field was not cleaned precisely:\n%s", got)
	}
}

func TestOpenAIChatToolResultToGeminiDoesNotDuplicateAsText(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"uapi\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"{\"ok\":true}"}
		]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	got := string(converted)
	if !strings.Contains(got, `"functionResponse"`) || strings.Contains(got, `"text":"{\"ok\":true}"`) {
		t.Fatalf("tool result must become only Gemini functionResponse:\n%s", got)
	}
}

func TestCrossProtocolResponseConversions(t *testing.T) {
	openAIResp := []byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	tests := []struct {
		name   string
		target convert.Format
		want   []string
	}{
		{name: "chat response to anthropic", target: convert.FormatAnthropic, want: []string{`"type":"message"`, `"content"`, `"usage"`, `"stop_reason":"end_turn"`}},
		{name: "chat response to gemini", target: convert.FormatGemini, want: []string{`"candidates"`, `"usageMetadata"`}},
		{name: "chat response to responses", target: convert.FormatOpenAIResponses, want: []string{`"object":"response"`, `"output"`, `"usage"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertResponse(convert.FormatOpenAIChatCompletions, tt.target, openAIResp)
			if err != nil {
				t.Fatalf("ConvertResponse: %v", err)
			}
			if !json.Valid(converted) {
				t.Fatalf("converted response is not valid JSON: %s", converted)
			}
			text := string(converted)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("converted response missing %s: %s", want, text)
				}
			}
		})
	}
}

func TestConvertRequestWithAdaptorUsesNewRegistry(t *testing.T) {
	adaptor := &anthropic.AnthropicAdaptor{}
	adaptor.Init(&db.Channel{Type: "anthropic", APIFormat: "standard"}, &db.Account{})
	body := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}]}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAnthropic, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	if !json.Valid(converted) {
		t.Fatalf("converted body is not valid JSON: %s", converted)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v", err)
	}
	if got["system"] != "be brief" {
		t.Fatalf("system = %#v, want be brief; body=%s", got["system"], converted)
	}
	if _, ok := got["messages"]; !ok {
		t.Fatalf("messages missing: %s", converted)
	}
}

func TestOpenAIChatToolChoiceToGeminiDoesNotEmitEmptyMode(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}],"tool_choice":{"type":"auto"}}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	tools, ok := got["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing: %s", converted)
	}
	functionDeclarations, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(functionDeclarations) != 1 {
		t.Fatalf("functionDeclarations missing: %s", converted)
	}
	declaration := functionDeclarations[0].(map[string]interface{})
	if declaration["name"] != "lookup" {
		t.Fatalf("function name = %#v, want lookup; body=%s", declaration["name"], converted)
	}
	if _, ok := declaration["parametersJsonSchema"]; !ok {
		t.Fatalf("parametersJsonSchema missing: %s", converted)
	}
	if _, ok := declaration["parameters"]; ok {
		t.Fatalf("parameters should not be emitted for OpenAI function tools: %s", converted)
	}
	toolConfig, ok := got["toolConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("toolConfig missing: %s", converted)
	}
	fcConfig, ok := toolConfig["functionCallingConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("functionCallingConfig missing: %s", converted)
	}
	if fcConfig["mode"] != "AUTO" {
		t.Fatalf("mode = %#v, want AUTO; body=%s", fcConfig["mode"], converted)
	}
}

func TestOpenAIChatToGeminiNormalizesRolesForPrivateAPIs(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"tool","tool_call_id":"lookup","content":"done"}]}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got struct {
		Contents []struct {
			Role string `json:"role"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	want := []string{"user", "model", "user"}
	if len(got.Contents) != len(want) {
		t.Fatalf("contents length = %d, want %d: %s", len(got.Contents), len(want), converted)
	}
	for i, role := range want {
		if got.Contents[i].Role != role {
			t.Fatalf("contents[%d].role = %q, want %q; body=%s", i, got.Contents[i].Role, role, converted)
		}
	}
}

func TestOpenAIChatToGeminiMapsResponseFormatToJSONMode(t *testing.T) {
	body := []byte(`{
		"model":"gemini-3.1-pro",
		"messages":[{"role":"user","content":"hello"}],
		"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}}}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	genConfig, ok := got["generationConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("generationConfig missing: %s", converted)
	}
	if genConfig["responseMimeType"] != "application/json" {
		t.Fatalf("responseMimeType = %#v, want application/json; body=%s", genConfig["responseMimeType"], converted)
	}
	schema, ok := genConfig["responseSchema"].(map[string]interface{})
	if !ok || schema["type"] != "object" {
		t.Fatalf("responseSchema not mapped from json_schema: %#v; body=%s", genConfig["responseSchema"], converted)
	}
}

func TestOpenAIChatToGeminiCapsExcessiveMaxTokens(t *testing.T) {
	body := []byte(`{"model":"gemini-3.1-pro","messages":[{"role":"user","content":"hello"}],"max_tokens":128000}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	genConfig := got["generationConfig"].(map[string]interface{})
	if genConfig["maxOutputTokens"] != float64(65536) {
		t.Fatalf("maxOutputTokens = %#v, want 65536; body=%s", genConfig["maxOutputTokens"], converted)
	}
}

func TestGeminiThinkingConfigNormalizesConflictingAliases(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{"thinkingConfig":{"thinking_budget":24576,"thinkingLevel":"HIGH","include_thoughts":true}}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	thinking := got["generationConfig"].(map[string]interface{})["thinkingConfig"].(map[string]interface{})
	if thinking["thinkingBudget"] != float64(24576) {
		t.Fatalf("thinkingBudget = %#v, want 24576; body=%s", thinking["thinkingBudget"], converted)
	}
	if thinking["includeThoughts"] != true {
		t.Fatalf("includeThoughts = %#v, want true; body=%s", thinking["includeThoughts"], converted)
	}
	if _, ok := thinking["thinkingLevel"]; ok {
		t.Fatalf("thinkingLevel should be removed when thinkingBudget is present: %s", converted)
	}
	if _, ok := thinking["thinking_budget"]; ok {
		t.Fatalf("snake_case thinking_budget should be normalized: %s", converted)
	}
}

func TestGeminiJSONModeSurvivesSameFormatConversion(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"object","properties":{"answer":{"type":"string"}}}}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	genConfig := got["generationConfig"].(map[string]interface{})
	if genConfig["responseMimeType"] != "application/json" {
		t.Fatalf("responseMimeType = %#v, want application/json; body=%s", genConfig["responseMimeType"], converted)
	}
	schema, ok := genConfig["responseSchema"].(map[string]interface{})
	if !ok || schema["type"] != "object" {
		t.Fatalf("responseSchema missing after same-format conversion: %#v; body=%s", genConfig["responseSchema"], converted)
	}
}

func TestGeminiFunctionDeclarationsSurviveConversion(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup","description":"Lookup data","parameters":{"type":"OBJECT","properties":{"query":{"type":"STRING"}},"required":["query"]}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	tools, ok := got["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing after conversion: %s", converted)
	}
	functionDeclarations, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(functionDeclarations) != 1 {
		t.Fatalf("functionDeclarations missing after conversion: %s", converted)
	}
	declaration := functionDeclarations[0].(map[string]interface{})
	if declaration["name"] != "lookup" {
		t.Fatalf("function declaration name = %#v, want lookup; body=%s", declaration["name"], converted)
	}
	if _, ok := declaration["parametersJsonSchema"].(map[string]interface{}); !ok {
		t.Fatalf("parametersJsonSchema missing after conversion: %s", converted)
	}
}

func TestAntigravityAdaptorNormalizesToolsForV1Internal(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string","format":"uri"}},"required":["query"]}}}],"tool_choice":{"type":"auto"}}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	tools, ok := request["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing: %s", converted)
	}
	functionDeclarations, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(functionDeclarations) != 1 {
		t.Fatalf("functionDeclarations missing: %s", converted)
	}
	declaration := functionDeclarations[0].(map[string]interface{})
	if _, ok := declaration["parametersJsonSchema"]; ok {
		t.Fatalf("parametersJsonSchema should be renamed for antigravity v1internal: %s", converted)
	}
	params, ok := declaration["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("parameters missing: %s", converted)
	}
	if params["type"] != "OBJECT" {
		t.Fatalf("parameters.type = %#v, want OBJECT; body=%s", params["type"], converted)
	}
	if _, ok := params["additionalProperties"]; ok {
		t.Fatalf("additionalProperties should be stripped for antigravity parameters: %s", converted)
	}
	properties := params["properties"].(map[string]interface{})
	query := properties["query"].(map[string]interface{})
	if query["type"] != "STRING" {
		t.Fatalf("query.type = %#v, want STRING; body=%s", query["type"], converted)
	}
	if _, ok := query["format"]; ok {
		t.Fatalf("nested format should be stripped for antigravity parameters: %s", converted)
	}
	toolConfig, ok := request["toolConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("toolConfig missing: %s", converted)
	}
	fcConfig, ok := toolConfig["functionCallingConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("functionCallingConfig missing: %s", converted)
	}
	if fcConfig["mode"] != "VALIDATED" {
		t.Fatalf("mode = %#v, want VALIDATED; body=%s", fcConfig["mode"], converted)
	}
}

func TestAntigravityAdaptorNormalizesGeminiRoles(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request := got["request"].(map[string]interface{})
	contents := request["contents"].([]interface{})
	second := contents[1].(map[string]interface{})
	if second["role"] != "model" {
		t.Fatalf("assistant role should be model for antigravity: %s", converted)
	}
}

func TestAntigravityAdaptorWithoutToolChoiceOmitsToolConfig(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{"model":"gpt-oss-120b-medium","messages":[{"role":"user","content":"hello"}],"stream":true}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIChatCompletions, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	if _, ok := request["toolConfig"]; ok {
		t.Fatalf("toolConfig should be omitted without tool_choice/tools: %s", converted)
	}
}

func TestOpenAIChatToolChoiceWithoutToolsOmitsGeminiToolConfig(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"tool_choice":{"type":"auto"}}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["toolConfig"]; ok {
		t.Fatalf("toolConfig should be omitted without tools: %s", converted)
	}
}

func TestCrossProtocolRequestConversionDropsSourceExtraFields(t *testing.T) {
	responsesBody := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["reasoning.encrypted_content"],
		"previous_response_id":"resp_1",
		"conversation":"conv_1",
		"metadata":{"trace":"abc"},
		"prompt_cache_key":"cache",
		"safety_identifier":"safe",
		"custom_source_only":true
	}`)
	chatBody := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"custom_source_only":true
	}`)

	tests := []struct {
		name   string
		source convert.Format
		target convert.Format
		body   []byte
		absent []string
	}{
		{
			name:   "responses to gemini",
			source: convert.FormatOpenAIResponses,
			target: convert.FormatGemini,
			body:   responsesBody,
			absent: []string{"include", "previous_response_id", "conversation", "metadata", "prompt_cache_key", "safety_identifier", "custom_source_only"},
		},
		{
			name:   "responses to anthropic",
			source: convert.FormatOpenAIResponses,
			target: convert.FormatAnthropic,
			body:   responsesBody,
			absent: []string{"include", "previous_response_id", "conversation", "metadata", "prompt_cache_key", "safety_identifier", "custom_source_only"},
		},
		{
			name:   "chat to gemini",
			source: convert.FormatOpenAIChatCompletions,
			target: convert.FormatGemini,
			body:   chatBody,
			absent: []string{"custom_source_only"},
		},
		{
			name:   "gemini to chat",
			source: convert.FormatGemini,
			target: convert.FormatOpenAIChatCompletions,
			body:   []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"custom_source_only":true}`),
			absent: []string{"custom_source_only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted, err := convert.ConvertRequest(tt.source, tt.target, tt.body)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(converted, &got); err != nil {
				t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
			}
			for _, key := range tt.absent {
				if containsJSONKey(got, key) {
					t.Fatalf("source-only key %q leaked into converted body: %s", key, converted)
				}
			}
		})
	}
}

func TestGeminiCLIEnvelopeExtraFieldsStayInsideCLIEnvelope(t *testing.T) {
	body := []byte(`{
		"model":"gemini-2.5-pro",
		"project":"project-1",
		"user_prompt_id":"prompt-1",
		"enabled_credit_types":["GOOGLE_ONE_AI"],
		"userAgent":"gemini-cli",
		"requestType":"generateContent",
		"requestId":"req-1",
		"sessionId":"sess-1",
		"request":{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"custom_inner_only":true}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGeminiCLI, convert.FormatGeminiCLI, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	credits, _ := got["enabled_credit_types"].([]interface{})
	if got["project"] != "project-1" || got["user_prompt_id"] != "prompt-1" || len(credits) != 1 || credits[0] != "GOOGLE_ONE_AI" || got["userAgent"] != "gemini-cli" || got["requestType"] != "generateContent" || got["sessionId"] != "sess-1" {
		t.Fatalf("CLI envelope fields not preserved: %s", converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	if containsJSONKey(request, "project") || containsJSONKey(request, "user_prompt_id") || containsJSONKey(request, "enabled_credit_types") || containsJSONKey(request, "userAgent") || containsJSONKey(request, "requestType") || containsJSONKey(request, "sessionId") {
		t.Fatalf("CLI envelope fields leaked into inner Gemini request: %s", converted)
	}
	if _, ok := request["custom_inner_only"]; !ok {
		t.Fatalf("same-format Gemini CLI inner extra should be preserved: %s", converted)
	}
}

func TestResponsesToOpenAIChatReordersToolOutputsAfterMatchingCalls(t *testing.T) {
	body := []byte(`{
		"model":"glm-5.1",
		"input":[
			{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"find .\"}","call_id":"call_1"},
			{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"rg oauth\"}","call_id":"call_2"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我先看一下。"}]},
			{"type":"function_call_output","call_id":"call_1","output":"find output"},
			{"type":"function_call_output","call_id":"call_2","output":"rg output"},
			{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat file\"}","call_id":"call_3"},
			{"type":"function_call_output","call_id":"call_3","output":"cat output"}
		],
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"stream":true
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	seenCalls := map[string]bool{}
	for i, msg := range got.Messages {
		if msg.Role == "assistant" && msg.Content == nil && len(msg.ToolCalls) == 0 {
			t.Fatalf("empty assistant message leaked at %d: %s", i, converted)
		}
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				seenCalls[call.ID] = true
			}
			continue
		}
		if msg.Role != "tool" {
			continue
		}
		if !seenCalls[msg.ToolCallID] {
			t.Fatalf("tool result %q appeared before matching tool call: %s", msg.ToolCallID, converted)
		}
		if i == 0 || got.Messages[i-1].Role != "assistant" && got.Messages[i-1].Role != "tool" {
			t.Fatalf("tool result %q is not grouped after tool calls: %s", msg.ToolCallID, converted)
		}
	}
	text := string(converted)
	if !(indexOrFatal(t, text, `"tool_call_id":"call_1"`) < indexOrFatal(t, text, `"我先看一下。"`)) {
		t.Fatalf("tool outputs should be moved before assistant text that originally separated them:\n%s", converted)
	}
}

func TestResponsesToOpenAIChatPreservesCachePassthroughFields(t *testing.T) {
	body := []byte(`{
		"model":"glm-5.1",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"prompt_cache_key":"019e9842-920e-70f1-8313-f2d2f848cd5a",
		"client_metadata":{"x-codex-installation-id":"0520e5f6-fd3f-4731-b006-465bf209ce33"},
		"safety_identifier":"safe_1",
		"conversation":"conv_should_not_leak",
		"stream":true
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var request map[string]interface{}
	if err := json.Unmarshal(converted, &request); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	for _, key := range []string{"prompt_cache_key", "client_metadata", "safety_identifier"} {
		if _, ok := request[key]; !ok {
			t.Fatalf("cache passthrough field %q missing: %s", key, converted)
		}
	}
	if streamOptions, _ := request["stream_options"].(map[string]interface{}); streamOptions["include_usage"] != true {
		t.Fatalf("stream_options.include_usage should be added for streamed Chat cache reporting: %s", converted)
	}
	if _, ok := request["conversation"]; ok {
		t.Fatalf("Responses-only field conversation leaked into Chat request: %s", converted)
	}
}

func TestAdaptorCrossProtocolConversionDropsSourceExtraFields(t *testing.T) {
	adaptor := &antigravity.AntigravityAdaptor{}
	adaptor.Init(&db.Channel{Type: "antigravity", APIFormat: "antigravity"}, &db.Account{})
	body := []byte(`{
		"model":"gpt-oss-120b-medium",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"conversation":"conv_1",
		"metadata":{"trace":"abc"},
		"prompt_cache_key":"cache",
		"custom_source_only":true
	}`)

	converted, err := provider.ConvertRequestWithAdaptor(provider.FormatOpenAIResponses, provider.FormatAntigravity, body, adaptor)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	for _, key := range []string{"conversation", "metadata", "prompt_cache_key", "custom_source_only"} {
		if containsJSONKey(got, key) {
			t.Fatalf("source-only key %q leaked into adaptor body: %s", key, converted)
		}
	}
}

func TestOpenAIToolCallsConvertToAnthropicProtocolObjects(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"messages":[
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"uapi\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"{\"result\":\"ok\"}"}
		]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	messages := got["messages"].([]interface{})
	assistantBlocks := messages[0].(map[string]interface{})["content"].([]interface{})
	toolUse := findObjectByType(assistantBlocks, "tool_use")
	if _, ok := toolUse["input"].(map[string]interface{}); !ok {
		t.Fatalf("Anthropic tool_use input must be an object: %s", converted)
	}
	toolResult := messages[1].(map[string]interface{})
	if toolResult["role"] != "user" {
		t.Fatalf("OpenAI tool role should become Anthropic user role: %s", converted)
	}
}

func TestOpenAIToolCallsConvertToGeminiProtocolObjects(t *testing.T) {
	body := []byte(`{
		"model":"gemini-3.1-pro",
		"messages":[
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"uapi\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"{\"result\":\"ok\"}"}
		]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	contents := got["contents"].([]interface{})
	callPart := findObjectWithKey(contents[0].(map[string]interface{})["parts"].([]interface{}), "functionCall")
	call := callPart["functionCall"].(map[string]interface{})
	if _, ok := call["args"].(map[string]interface{}); !ok {
		t.Fatalf("Gemini functionCall args must be an object: %s", converted)
	}
	responsePart := findObjectWithKey(contents[1].(map[string]interface{})["parts"].([]interface{}), "functionResponse")
	response := responsePart["functionResponse"].(map[string]interface{})
	if response["name"] != "lookup" {
		t.Fatalf("Gemini functionResponse name should use original tool name: %s", converted)
	}
}

func TestAnthropicThinkingSignatureSurvivesResponseConversions(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"content":[{"type":"thinking","thinking":"think","signature":"sig_1"},{"type":"text","text":"answer"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":4}
	}`)

	chat, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Anthropic -> Chat: %v", err)
	}
	for _, want := range []string{`"reasoning_content":"think"`, `"reasoning_details"`, `"signature":"sig_1"`} {
		if !strings.Contains(string(chat), want) {
			t.Fatalf("Anthropic thinking signature missing from Chat response, missing %s:\n%s", want, chat)
		}
	}

	gemini, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("Anthropic -> Gemini: %v", err)
	}
	for _, want := range []string{`"thought":true`, `"text":"think"`, `"thoughtSignature":"sig_1"`} {
		if !strings.Contains(string(gemini), want) {
			t.Fatalf("Anthropic thinking signature missing from Gemini response, missing %s:\n%s", want, gemini)
		}
	}
}

func TestResponseConversionPreservesInterleavedItemOrder(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"content":[
			{"type":"text","text":"before"},
			{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"uapi"}},
			{"type":"text","text":"after"}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":3,"output_tokens":4}
	}`)

	gemini, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("Anthropic -> Gemini: %v", err)
	}
	text := string(gemini)
	before := indexOrFatal(t, text, `"text":"before"`)
	call := indexOrFatal(t, text, `"functionCall"`)
	after := indexOrFatal(t, text, `"text":"after"`)
	if !(before < call && call < after) {
		t.Fatalf("interleaved response items were reordered:\n%s", gemini)
	}

	responses, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("Anthropic -> Responses: %v", err)
	}
	text = string(responses)
	before = indexOrFatal(t, text, `"text":"before"`)
	call = indexOrFatal(t, text, `"type":"function_call"`)
	after = indexOrFatal(t, text, `"text":"after"`)
	if !(before < call && call < after) {
		t.Fatalf("Responses output item order changed:\n%s", responses)
	}
}

func TestResponsesReasoningEncryptedContentSurvivesResponseConversions(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"output":[
			{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"enc_1"},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}
		],
		"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}
	}`)

	chat, err := convert.ConvertResponse(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Responses -> Chat: %v", err)
	}
	for _, want := range []string{`"reasoning_content":"think"`, `"encrypted_content":"enc_1"`, `"data":"enc_1"`} {
		if !strings.Contains(string(chat), want) {
			t.Fatalf("Responses encrypted reasoning missing from Chat response, missing %s:\n%s", want, chat)
		}
	}

	gemini, err := convert.ConvertResponse(convert.FormatOpenAIResponses, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("Responses -> Gemini: %v", err)
	}
	if !strings.Contains(string(gemini), `"thoughtSignature":"enc_1"`) {
		t.Fatalf("Responses encrypted reasoning missing from Gemini response:\n%s", gemini)
	}

	anthropic, err := convert.ConvertResponse(convert.FormatOpenAIResponses, convert.FormatAnthropic, body)
	if err != nil {
		t.Fatalf("Responses -> Anthropic: %v", err)
	}
	if !strings.Contains(string(anthropic), `"type":"redacted_thinking"`) || !strings.Contains(string(anthropic), `"data":"enc_1"`) {
		t.Fatalf("Responses encrypted reasoning missing from Anthropic redacted thinking:\n%s", anthropic)
	}
}

func TestReasoningConfigMapsToNativeThinkingConfigs(t *testing.T) {
	chatBody := []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`)
	gemini, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, chatBody)
	if err != nil {
		t.Fatalf("Chat -> Gemini: %v", err)
	}
	if !strings.Contains(string(gemini), `"thinkingLevel":"HIGH"`) || !strings.Contains(string(gemini), `"includeThoughts":true`) {
		t.Fatalf("OpenAI reasoning_effort not mapped to Gemini thinkingConfig:\n%s", gemini)
	}
	anthropic, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatAnthropic, chatBody)
	if err != nil {
		t.Fatalf("Chat -> Anthropic: %v", err)
	}
	if !strings.Contains(string(anthropic), `"thinking":{"type":"enabled"}`) {
		t.Fatalf("OpenAI reasoning_effort not mapped to Anthropic thinking:\n%s", anthropic)
	}

	responsesBody := []byte(`{"model":"model","input":"hi","reasoning":{"max_tokens":8192}}`)
	anthropic, err = convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatAnthropic, responsesBody)
	if err != nil {
		t.Fatalf("Responses -> Anthropic: %v", err)
	}
	if !strings.Contains(string(anthropic), `"budget_tokens":8192`) {
		t.Fatalf("Responses reasoning max_tokens not mapped to Anthropic thinking budget:\n%s", anthropic)
	}
}

func TestResponsesPDFInputFileConvertsToOpenAIChatImageURL(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[{"type":"message","role":"user","content":[
			{"type":"input_text","text":"summarize"},
			{"type":"input_file","file_data":"data:application/pdf;base64,AA==","filename":"paper.pdf","file_type":"application/pdf"}
		]}],
		"temperature":"[undefined]"
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Responses -> Chat: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["temperature"]; ok {
		t.Fatalf("undefined sentinel was not removed before conversion: %s", converted)
	}
	messages := got["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].([]interface{})
	image := content[1].(map[string]interface{})
	if image["type"] != "image_url" {
		t.Fatalf("PDF input_file was not converted to Chat image_url block: %s", converted)
	}
	imageBody := image["image_url"].(map[string]interface{})
	if imageBody["url"] != "data:application/pdf;base64,AA==" {
		t.Fatalf("PDF image_url block lost data URI: %s", converted)
	}
	if _, ok := image["file"]; ok {
		t.Fatalf("PDF Chat block must not emit file payload for this compatible path: %s", converted)
	}
}

func TestTypelessResponsesMessagePDFInputFileConvertsToOpenAIChatImageURL(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":[
			{"type":"input_text","text":"summarize"},
			{"type":"input_file","file_data":"data:application/pdf;base64,AA==","filename":"paper.pdf","file_type":"application/pdf"}
		]}],
		"temperature":"[undefined]",
		"tools":"[undefined]"
	}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Responses -> Chat: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if strings.Contains(string(converted), "[undefined]") {
		t.Fatalf("undefined sentinel survived conversion: %s", converted)
	}
	messages := got["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].([]interface{})
	image := content[1].(map[string]interface{})
	if image["type"] != "image_url" {
		t.Fatalf("typeless Responses message PDF input_file was not converted to Chat image_url block: %s", converted)
	}
	imageBody := image["image_url"].(map[string]interface{})
	if imageBody["url"] != "data:application/pdf;base64,AA==" {
		t.Fatalf("typeless Responses PDF image_url block lost data URI: %s", converted)
	}
	if _, ok := image["file"]; ok {
		t.Fatalf("typeless Responses PDF Chat block must not emit file payload for this compatible path: %s", converted)
	}
}

func TestGeminiPDFInlineDataConvertsToOpenAIChatImageURL(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[
			{"text":"summarize"},
			{"inlineData":{"mimeType":"application/pdf","data":"AA=="}}
		]}],
		"generationConfig":{"maxOutputTokens":"[undefined]"}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Gemini -> Chat: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["max_tokens"]; ok {
		t.Fatalf("undefined maxOutputTokens should not become max_tokens: %s", converted)
	}
	messages := got["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].([]interface{})
	image := content[1].(map[string]interface{})
	if image["type"] != "image_url" {
		t.Fatalf("PDF inlineData was not converted to Chat image_url block: %s", converted)
	}
	imageBody := image["image_url"].(map[string]interface{})
	if imageBody["url"] != "data:application/pdf;base64,AA==" {
		t.Fatalf("Gemini PDF image_url block lost data URI: %s", converted)
	}
	if _, ok := image["file"]; ok {
		t.Fatalf("Gemini PDF Chat block must not emit file payload for this compatible path: %s", converted)
	}
}

func TestGeminiSnakeCasePDFInlineDataConvertsToOpenAIChatImageURL(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[
			{"text":"summarize"},
			{"inline_data":{"mime_type":"application/pdf","data":"AA=="}}
		]}],
		"generationConfig":{"maxOutputTokens":"[undefined]"}
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Gemini -> Chat: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	messages := got["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].([]interface{})
	image := content[1].(map[string]interface{})
	if image["type"] != "image_url" {
		t.Fatalf("snake_case Gemini inline_data PDF was not converted to Chat image_url block: %s", converted)
	}
	imageBody := image["image_url"].(map[string]interface{})
	if imageBody["url"] != "data:application/pdf;base64,AA==" {
		t.Fatalf("snake_case Gemini PDF image_url block lost data URI: %s", converted)
	}
	if _, ok := image["file"]; ok {
		t.Fatalf("snake_case Gemini PDF Chat block must not emit file payload for this compatible path: %s", converted)
	}
	if strings.Contains(string(converted), "inline_data") || strings.Contains(string(converted), "mime_type") {
		t.Fatalf("Gemini native snake_case fields leaked into Chat body: %s", converted)
	}
}

func TestGeminiInjectedModelSurvivesCodexConversion(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"contents":[{"role":"user","parts":[{"text":"hello"}]}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatCodexResponses, body)
	if err != nil {
		t.Fatalf("Gemini -> Codex: %v", err)
	}
	if !strings.Contains(string(converted), `"model":"gpt-5.5"`) {
		t.Fatalf("Gemini injected model was not preserved for Codex: %s", converted)
	}
}

func TestAnthropicDocumentToOpenAIResponsesOmitsUnsupportedFileType(t *testing.T) {
	body := []byte(`{"model":"claude","max_tokens":100,"messages":[{"role":"user","content":[
		{"type":"text","text":"summarize"},
		{"type":"document","title":"paper.pdf","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}
	]}]}`)
	converted, err := convert.ConvertRequest(convert.FormatAnthropic, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("Anthropic -> Responses: %v", err)
	}
	text := string(converted)
	if !strings.Contains(text, `"type":"input_file"`) || !strings.Contains(text, `"file_data":"data:application/pdf;base64,AA=="`) {
		t.Fatalf("Anthropic document did not become Responses input_file: %s", converted)
	}
	if strings.Contains(text, `"file_type"`) || strings.Contains(text, `"mime_type"`) || strings.Contains(text, `"title"`) {
		t.Fatalf("Responses input_file must not include unsupported native fields: %s", converted)
	}
	if !strings.Contains(text, `"filename":"paper.pdf"`) {
		t.Fatalf("Responses input_file should preserve Anthropic document title as filename: %s", converted)
	}
}

func TestAnthropicCacheControlNotEmittedToOpenAIResponsesContent(t *testing.T) {
	body := []byte(`{"model":"claude","max_tokens":100,"messages":[{"role":"user","content":[
		{"type":"text","text":"cache me","cache_control":{"type":"ephemeral"}},
		{"type":"text","text":"then answer"}
	]}]}`)
	converted, audit, err := convert.ConvertRequestDetailed(convert.FormatAnthropic, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("Anthropic -> Responses: %v", err)
	}
	if strings.Contains(string(converted), `"cache_control"`) {
		t.Fatalf("Responses content must not include Anthropic cache_control: %s", converted)
	}
	if audit == nil || len(audit.Turns) == 0 || len(audit.Turns[0].Items) == 0 {
		t.Fatalf("audit missing turns/items: %#v", audit)
	}
	if !hasLossField(audit.Turns[0].Items[0].Losses, "cache_control") {
		t.Fatalf("cache_control loss missing from item audit: %#v", audit.Turns[0].Items[0].Losses)
	}
}

func TestOpenAIResponsesUsageCachedTokensConvertsToChatUsage(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":7}}
	}`)
	converted, err := convert.ConvertResponse(convert.FormatOpenAIResponses, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Responses -> Chat response: %v", err)
	}
	var chat struct {
		Usage struct {
			PromptTokensDetails map[string]interface{} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatalf("decode Chat response: %v; body=%s", err, converted)
	}
	if chat.Usage.PromptTokensDetails["cached_tokens"] != float64(7) {
		t.Fatalf("cached input tokens not preserved as chat cached_tokens: %s", converted)
	}
}

func TestAnthropicCacheCreationUsageConvertsToChatCachedWriteTokens(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":20,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}
	}`)
	converted, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Anthropic -> Chat response: %v", err)
	}
	var chat struct {
		Usage struct {
			PromptTokensDetails map[string]interface{} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatalf("decode Chat response: %v; body=%s", err, converted)
	}
	if chat.Usage.PromptTokensDetails["cached_tokens"] != float64(7) {
		t.Fatalf("cache read tokens not emitted as cached_tokens: %#v; body=%s", chat.Usage.PromptTokensDetails, converted)
	}
	if chat.Usage.PromptTokensDetails["cached_write_tokens"] != float64(5) {
		t.Fatalf("cache creation tokens not emitted as cached_write_tokens: %#v; body=%s", chat.Usage.PromptTokensDetails, converted)
	}
}

func TestAnthropicCacheUsageConvertsToGeminiCachedContentReadTokens(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":20,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}
	}`)
	converted, err := convert.ConvertResponse(convert.FormatAnthropic, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("Anthropic -> Gemini response: %v", err)
	}
	var resp struct {
		UsageMetadata struct {
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(converted, &resp); err != nil {
		t.Fatalf("decode Gemini response: %v; body=%s", err, converted)
	}
	if resp.UsageMetadata.CachedContentTokenCount != 7 {
		t.Fatalf("Gemini cachedContentTokenCount = %d, want cache read tokens 7; body=%s", resp.UsageMetadata.CachedContentTokenCount, converted)
	}
}

func TestGeminiPDFInlineDataConvertsToOpenAIResponsesInputFile(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[
			{"text":"summarize"},
			{"inlineData":{"mimeType":"application/pdf","data":"AA=="}}
		]}],
		"generationConfig":{"maxOutputTokens":"[undefined]"},
		"systemInstruction":"[undefined]",
		"tools":"[undefined]"
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("Gemini -> Responses: %v", err)
	}
	text := string(converted)
	for _, want := range []string{
		`"type":"input_file"`,
		`"file_data":"data:application/pdf;base64,AA=="`,
		`"filename":"input.pdf"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Gemini PDF inlineData missing %s in Responses body: %s", want, converted)
		}
	}
	for _, rejected := range []string{`"[undefined]"`, `"file_type"`, `"mime_type"`, `"title"`} {
		if strings.Contains(text, rejected) {
			t.Fatalf("Gemini PDF Responses body contains unsupported %s: %s", rejected, converted)
		}
	}
}

func TestOpenAIChatFileConvertsToGeminiInlineData(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":[
		{"type":"text","text":"summarize"},
		{"type":"file","file":{"file_data":"data:application/pdf;base64,AA==","filename":"paper.pdf"}}
	]}]}`)
	converted, err := convert.ConvertRequest(convert.FormatOpenAIChatCompletions, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("Chat -> Gemini: %v", err)
	}
	if !strings.Contains(string(converted), `"inlineData":{"data":"AA==","mimeType":"application/pdf"}`) {
		t.Fatalf("Chat file did not become Gemini inlineData PDF: %s", converted)
	}
}

func TestAnthropicPDFDocumentConvertsToOpenAIChatImageURL(t *testing.T) {
	body := []byte(`{"model":"claude","max_tokens":100,"messages":[{"role":"user","content":[
		{"type":"text","text":"summarize","cache_control":"[undefined]"},
		{"type":"document","title":"paper.pdf","cache_control":"[undefined]","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}
	]}]}`)
	converted, err := convert.ConvertRequest(convert.FormatAnthropic, convert.FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Anthropic -> Chat: %v", err)
	}
	if !strings.Contains(string(converted), `"type":"image_url"`) ||
		!strings.Contains(string(converted), `"url":"data:application/pdf;base64,AA=="`) {
		t.Fatalf("Anthropic document did not become Chat image_url block: %s", converted)
	}
	for _, rejected := range []string{`"[undefined]"`, `"cache_control"`, `"source"`, `"title"`, `"file"`} {
		if strings.Contains(string(converted), rejected) {
			t.Fatalf("Anthropic native/undefined field leaked into Chat body (%s): %s", rejected, converted)
		}
	}
}

func findObjectByType(items []interface{}, typ string) map[string]interface{} {
	for _, item := range items {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == typ {
			return obj
		}
	}
	return nil
}

func findObjectWithKey(items []interface{}, key string) map[string]interface{} {
	for _, item := range items {
		obj, _ := item.(map[string]interface{})
		if _, ok := obj[key]; ok {
			return obj
		}
	}
	return nil
}

func containsJSONKey(v interface{}, key string) bool {
	switch typed := v.(type) {
	case map[string]interface{}:
		if _, ok := typed[key]; ok {
			return true
		}
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}

func indexOrFatal(t *testing.T, text, needle string) int {
	t.Helper()
	idx := strings.Index(text, needle)
	if idx < 0 {
		t.Fatalf("missing %s in %s", needle, text)
	}
	return idx
}

func TestResponsesFunctionCallOutputStructuredOutputPreservedInIRAndResponses(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","call_id":"call_1","output":{"items":[{"type":"text","text":"ok"}],"count":2}}]}`)
	req, err := convert.ToIR(convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ToIR: %v", err)
	}
	if len(req.Turns) != 1 || len(req.Turns[0].Items) != 1 || req.Turns[0].Items[0].ToolResult == nil {
		t.Fatalf("tool result missing from IR: %#v", req.Turns)
	}
	if got := string(req.Turns[0].Items[0].ToolResult.OutputRaw); !strings.Contains(got, `"count":2`) {
		t.Fatalf("structured output raw not preserved: %s", got)
	}
	out, err := convert.ConvertRequest(convert.FormatOpenAIResponses, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	if !strings.Contains(string(out), `"output":{`) || !strings.Contains(string(out), `"count":2`) {
		t.Fatalf("structured output not emitted as JSON object: %s", out)
	}
}
