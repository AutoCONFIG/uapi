package provider_conversion_test

import (
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	_ "github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/stream"
)

func convertGeminiStream(t *testing.T, input string) string {
	t.Helper()
	converter := stream.NewConverter(convert.FormatGemini, convert.FormatOpenAIChatCompletions)
	if converter == nil {
		t.Fatalf("missing Gemini stream converter")
	}
	return string(converter.Convert([]byte(input)))
}

func TestGeminiStreamIgnoresThoughtSignatureMetadata(t *testing.T) {
	got := convertGeminiStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"hi","thoughtSignature":"opaque"},{"thought":true,"thoughtSignature":"opaque"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":5}}`+"\n\n")
	if strings.Contains(got, `"object":"error"`) || strings.Contains(got, "thoughtSignature") {
		t.Fatalf("Gemini thought metadata must not leak as a conversion error: %s", got)
	}
	if !strings.Contains(got, `"content":"hi"`) || !strings.Contains(got, `"prompt_tokens":3`) || !strings.Contains(got, `"completion_tokens":5`) {
		t.Fatalf("Gemini stream conversion must preserve text and usage while ignoring thought metadata: %s", got)
	}
}

func TestGeminiStreamMapsThoughtTextToReasoningContent(t *testing.T) {
	got := convertGeminiStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"thinking","thought":true},{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}}`+"\n\n")
	if strings.Contains(got, `"object":"error"`) {
		t.Fatalf("Gemini thought text must not produce conversion error: %s", got)
	}
	if !strings.Contains(got, `"reasoning_content":"thinking"`) || !strings.Contains(got, `"reasoning":"thinking"`) || !strings.Contains(got, `"content":"answer"`) {
		t.Fatalf("Gemini thought and answer text must be separated: %s", got)
	}
}

func TestGeminiStreamConvertsFunctionResponseAndCodeParts(t *testing.T) {
	got := convertGeminiStream(t, `data: {"candidates":[{"content":{"parts":[{"functionResponse":{"name":"lookup","response":{"content":[{"text":"tool result"}]}}},{"executableCode":{"language":"python","code":"print(1)"}},{"codeExecutionResult":{"output":"1\n"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}}`+"\n\n")
	if strings.Contains(got, `"object":"error"`) {
		t.Fatalf("Gemini executable/function response parts must not produce conversion error: %s", got)
	}
	for _, want := range []string{"tool result", "```python", "print(1)", "1\\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Gemini part conversion lost %q: %s", want, got)
		}
	}
}

func TestGeminiResponseSkipsThoughtSignatureMetadata(t *testing.T) {
	out, err := provider.ConvertResponse(provider.FormatGemini, provider.FormatOpenAIChatCompletions, []byte(`{
		"candidates":[{"content":{"parts":[{"text":"thinking","thought":true,"thoughtSignature":"opaque"},{"thoughtSignature":"opaque"},{"text":"answer"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":4}
	}`))
	if err != nil {
		t.Fatalf("ConvertResponse must ignore Gemini thought metadata: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "thoughtSignature") || strings.Contains(got, `"object":"error"`) {
		t.Fatalf("Gemini thought metadata must not leak as conversion output: %s", got)
	}
	if !strings.Contains(got, `"content":"answer"`) || !strings.Contains(got, `"reasoning_content":"thinking"`) || !strings.Contains(got, `"prompt_tokens":2`) || !strings.Contains(got, `"completion_tokens":4`) {
		t.Fatalf("Gemini converted response lost content, reasoning, or usage: %s", got)
	}
}

func TestOpenAIChatResponsePreservesReasoningOutsideContent(t *testing.T) {
	out, err := provider.ConvertResponse(provider.FormatOpenAIChatCompletions, provider.FormatOpenAIChatCompletions, []byte(`{
		"id":"chatcmpl-test",
		"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"thinking"},"finish_reason":"stop"}]
	}`))
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"content":"answer"`) || !strings.Contains(got, `"reasoning_content":"thinking"`) || !strings.Contains(got, `"reasoning":"thinking"`) {
		t.Fatalf("OpenAI Chat output must preserve reasoning fields: %s", got)
	}
}
