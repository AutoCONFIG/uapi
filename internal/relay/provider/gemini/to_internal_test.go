package gemini

import (
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

func TestGeminiRejectsNonFunctionToolsForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"tools":[{"googleSearch":{}}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "googleSearch") {
		t.Fatalf("expected explicit non-function tool rejection, got %v", err)
	}
}

func TestGeminiParsesFunctionToolsAcrossEntries(t *testing.T) {
	ir, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"tools":[
			{"functionDeclarations":[{"name":"lookup","description":"lookup data","parameters":{"type":"object"}}]},
			{"functionDeclarations":[{"name":"search","description":"search data","parameters":{"type":"object"}}]}
		]
	}`))
	if err != nil {
		t.Fatalf("geminiToInternal: %v", err)
	}
	if len(ir.Tools) != 2 || ir.Tools[0].Name != "lookup" || ir.Tools[1].Name != "search" {
		t.Fatalf("function tools were not parsed across all entries: %+v", ir.Tools)
	}
}

func TestGeminiRejectsUnsupportedTopLevelFieldsForCrossFormat(t *testing.T) {
	// Known top-level fields (safetySettings, provider) are now handled explicitly
	// and stored in dedicated fields. Unknown fields go to ExtraParams.
	req, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"safetySettings":[{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"BLOCK_NONE"}]
	}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Verify safetySettings is captured in the dedicated SafetySettings field
	if req.SafetySettings == nil {
		t.Fatal("expected SafetySettings to be populated")
	}
	// ExtraParams should be empty since safetySettings is an explicit field
	if len(req.ExtraParams) > 0 {
		t.Fatalf("expected ExtraParams to be empty for known field, got %v", req.ExtraParams)
	}
}

func TestGeminiStoresUnknownFieldsInExtraParams(t *testing.T) {
	// Test that truly unknown fields go to ExtraParams
	req, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"unknownField":"some value",
		"anotherUnknown":123
	}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if req.ExtraParams == nil {
		t.Fatal("expected ExtraParams to be populated")
	}
	// Just check the keys exist - interface{} comparison is tricky
	if _, ok := req.ExtraParams["unknownField"]; !ok {
		t.Fatalf("expected unknownField in ExtraParams, got %v", req.ExtraParams)
	}
	if _, ok := req.ExtraParams["anotherUnknown"]; !ok {
		t.Fatalf("expected anotherUnknown in ExtraParams, got %v", req.ExtraParams)
	}
}

func TestGeminiRejectsMalformedInlineDataForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png"}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "inlineData") {
		t.Fatalf("expected explicit malformed inlineData rejection, got %v", err)
	}
}

func TestGeminiRejectsUnsupportedGenerationConfigForCrossFormat(t *testing.T) {
	// Unknown generationConfig fields are now stored in ExtraParams with prefix
	// This test verifies the new behavior: unknown fields should be in ExtraParams
	req, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"generationConfig":{"responseMimeType":"application/json"}
	}`))
	if err != nil {
		t.Fatalf("expected no error for unknown field stored in ExtraParams, got %v", err)
	}
	// Verify responseMimeType is captured in ExtraParams with generationConfig. prefix
	if req.ExtraParams == nil {
		t.Fatal("expected ExtraParams to be populated")
	}
	if _, ok := req.ExtraParams["generationConfig.responseMimeType"]; !ok {
		t.Fatalf("expected generationConfig.responseMimeType in ExtraParams, got %v", req.ExtraParams)
	}
}

func TestGeminiRejectsMultipleAllowedFunctionNamesForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["a","b"]}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "allowedFunctionNames") {
		t.Fatalf("expected explicit toolConfig rejection, got %v", err)
	}
}

func TestGeminiRejectsMalformedSystemInstructionForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"systemInstruction":{"parts":[{"text":"ok","inlineData":{"mimeType":"image/png","data":"abc"}}]},
		"contents":[{"role":"user","parts":[{"text":"你好"}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "systemInstruction") {
		t.Fatalf("expected explicit malformed systemInstruction rejection, got %v", err)
	}
}

func TestGeminiRejectsMalformedFunctionDeclarationsForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"tools":[{"functionDeclarations":[{"description":"missing name"}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected explicit malformed functionDeclaration rejection, got %v", err)
	}
}

func TestGeminiRejectsMalformedContentsForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":{"role":"user","parts":[{"text":"你好"}]}
	}`))
	if err == nil || !strings.Contains(err.Error(), "contents") {
		t.Fatalf("expected explicit malformed contents rejection, got %v", err)
	}
}

func TestGeminiRejectsMixedPartFieldsForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好","inlineData":{"mimeType":"image/png","data":"abc"}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected explicit mixed part rejection, got %v", err)
	}
}

func TestGeminiRejectsFunctionCallWithoutNameForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"model","parts":[{"functionCall":{"args":{}}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "functionCall requires name") {
		t.Fatalf("expected explicit functionCall name rejection, got %v", err)
	}
}

func TestGeminiRejectsNestedUnknownFieldsForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"systemInstruction":{"parts":[{"text":"ok"}],"unknown":true},
		"contents":[{"role":"user","parts":[{"text":"你好"}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "systemInstruction") {
		t.Fatalf("expected explicit systemInstruction unknown field rejection, got %v", err)
	}

	_, err = geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}],"unknown":true}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "gemini content") {
		t.Fatalf("expected explicit content unknown field rejection, got %v", err)
	}

	_, err = geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup","x-extra":true}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "functionDeclaration") {
		t.Fatalf("expected explicit functionDeclaration unknown field rejection, got %v", err)
	}
}

func TestGeminiRejectsMalformedContainerTypesForCrossFormat(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"generationConfig":"bad"
	}`))
	if err == nil || !strings.Contains(err.Error(), "generationConfig") {
		t.Fatalf("expected explicit generationConfig type rejection, got %v", err)
	}

	_, err = geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"generationConfig":{"stopSequences":"bad"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "stopSequences") {
		t.Fatalf("expected explicit stopSequences type rejection, got %v", err)
	}

	_, err = geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"tools":{"functionDeclarations":[]}
	}`))
	if err == nil || !strings.Contains(err.Error(), "tools") {
		t.Fatalf("expected explicit tools type rejection, got %v", err)
	}

	_, err = geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[{"text":"你好"}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":"lookup"}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "allowedFunctionNames") {
		t.Fatalf("expected explicit allowedFunctionNames type rejection, got %v", err)
	}
}

func TestGeminiCodeAssistResponseArrayWrapper(t *testing.T) {
	ir, err := geminiResponseToInternal([]byte(`{
		"response":[{
			"modelVersion":"gemini-test",
			"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}
		}]
	}`))
	if err != nil {
		t.Fatalf("geminiResponseToInternal: %v", err)
	}
	if ir.Model != "gemini-test" || len(ir.Choices) != 1 || len(ir.Choices[0].Message.Content) != 1 || ir.Choices[0].Message.Content[0].Text != "hi" {
		t.Fatalf("CodeAssist array wrapper was not converted correctly: %+v", ir)
	}
	if ir.Usage.PromptTokens != 1 || ir.Usage.CompletionTokens != 2 {
		t.Fatalf("usage was not preserved from CodeAssist array wrapper: %+v", ir.Usage)
	}
}

func TestGeminiToolArgumentsPreserveLargeNumberPrecision(t *testing.T) {
	ir, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"id":9007199254740993}}}]}]
	}`))
	if err != nil {
		t.Fatalf("geminiToInternal: %v", err)
	}
	if got := ir.Messages[0].ToolCalls[0].Arguments; !strings.Contains(got, "9007199254740993") {
		t.Fatalf("tool arguments lost numeric precision: %s", got)
	}
}

func TestInternalToGeminiResponseRejectsInvalidToolArguments(t *testing.T) {
	_, err := internalToGeminiResponse(&provider.InternalResponse{
		Choices: []provider.InternalChoice{{
			Message: provider.InternalMessage{
				ToolCalls: []provider.InternalToolCall{{Name: "lookup", Arguments: "{bad"}},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid tool arguments rejection, got %v", err)
	}
}

func TestInternalToGeminiPreservesLargeNumberToolArguments(t *testing.T) {
	out, err := internalToGemini(&provider.InternalRequest{
		Model: "gemini-test",
		Messages: []provider.InternalMessage{{
			Role: "assistant",
			ToolCalls: []provider.InternalToolCall{{
				Name:      "lookup",
				Arguments: `{"id":9007199254740993}`,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("internalToGemini: %v", err)
	}
	if !strings.Contains(string(out), "9007199254740993") {
		t.Fatalf("tool arguments lost numeric precision: %s", out)
	}
}

func TestGeminiResponseRejectsMalformedFunctionCallPart(t *testing.T) {
	_, err := geminiResponseToInternal([]byte(`{
		"candidates":[{"content":{"parts":[{"functionCall":"bad"}]},"finishReason":"STOP"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "functionCall") {
		t.Fatalf("expected malformed functionCall rejection, got %v", err)
	}
}

func TestGeminiRejectsMultipleFunctionResponsesInOneContent(t *testing.T) {
	_, err := geminiToInternal([]byte(`{
		"model":"gemini-test",
		"contents":[{"role":"user","parts":[
			{"functionResponse":{"name":"lookup","id":"call_1","response":{"content":[{"text":"one"}]}}},
			{"functionResponse":{"name":"lookup","id":"call_2","response":{"content":[{"text":"two"}]}}}
		]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "multiple functionResponse") {
		t.Fatalf("expected multiple functionResponse rejection, got %v", err)
	}
}
