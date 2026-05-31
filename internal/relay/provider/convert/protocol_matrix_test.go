package convert_test

import (
	"encoding/json"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func TestRequestProtocolMatrixEmitsValidJSON(t *testing.T) {
	fixtures := []struct {
		format convert.Format
		body   []byte
	}{
		{convert.FormatOpenAIChatCompletions, []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"brief"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],"tool_choice":"auto"}`)},
		{convert.FormatOpenAIResponses, []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}],"tool_choice":"auto"}`)},
		{convert.FormatCodexResponses, []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"store":false}`)},
		{convert.FormatAnthropic, []byte(`{"model":"claude-test","max_tokens":64,"system":[{"type":"text","text":"brief"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}],"tool_choice":{"type":"auto"}}`)},
		{convert.FormatClaudeCode, []byte(`{"model":"claude-test","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`)},
		{convert.FormatGemini, []byte(`{"model":"gemini-test","contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":{"type":"object","properties":{"q":{"type":"string"}}}}]}]}`)},
		{convert.FormatGeminiCLI, []byte(`{"model":"gemini-test","project":"p","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`)},
		{convert.FormatAntigravity, []byte(`{"model":"gpt-oss-test","userAgent":"antigravity","requestType":"generateContent","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`)},
	}

	for _, source := range fixtures {
		for _, target := range fixtures {
			t.Run(string(source.format)+"_to_"+string(target.format), func(t *testing.T) {
				converted, err := convert.ConvertRequest(source.format, target.format, source.body)
				if err != nil {
					t.Fatalf("ConvertRequest: %v", err)
				}
				if !json.Valid(converted) {
					t.Fatalf("converted request is not valid JSON: %s", converted)
				}
			})
		}
	}
}

func TestResponseProtocolMatrixEmitsValidJSON(t *testing.T) {
	fixtures := []struct {
		format convert.Format
		body   []byte
	}{
		{convert.FormatOpenAIChatCompletions, []byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)},
		{convert.FormatOpenAIResponses, []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)},
		{convert.FormatCodexResponses, []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)},
		{convert.FormatAnthropic, []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)},
		{convert.FormatClaudeCode, []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)},
		{convert.FormatGemini, []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)},
		{convert.FormatGeminiCLI, []byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}`)},
		{convert.FormatAntigravity, []byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}`)},
	}

	for _, source := range fixtures {
		for _, target := range fixtures {
			t.Run(string(source.format)+"_to_"+string(target.format), func(t *testing.T) {
				converted, err := convert.ConvertResponse(source.format, target.format, source.body)
				if err != nil {
					t.Fatalf("ConvertResponse: %v", err)
				}
				if !json.Valid(converted) {
					t.Fatalf("converted response is not valid JSON: %s", converted)
				}
			})
		}
	}
}
