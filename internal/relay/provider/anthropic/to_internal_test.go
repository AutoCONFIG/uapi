package anthropic

import (
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

func TestAnthropicRejectsServerToolsForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "web_search_20250305") {
		t.Fatalf("expected explicit server tool rejection, got %v", err)
	}
}

func TestAnthropicAllowsCustomTools(t *testing.T) {
	ir, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好"}],
		"tools":[{"type":"custom","name":"lookup","description":"lookup data","input_schema":{"type":"object"}}]
	}`))
	if err != nil {
		t.Fatalf("anthropicToInternal: %v", err)
	}
	if len(ir.Tools) != 1 || ir.Tools[0].Name != "lookup" {
		t.Fatalf("custom tool was not parsed: %+v", ir.Tools)
	}
}

func TestAnthropicAllowsThinkingForCrossFormat(t *testing.T) {
	ir, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[{"role":"user","content":"你好"}]
	}`))
	if err != nil {
		t.Fatalf("thinking should be allowed, got error: %v", err)
	}
	if ir.Thinking == nil {
		t.Fatal("thinking field should be parsed into InternalRequest")
	}
}

func TestAnthropicRejectsUnknownToolChoiceForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好"}],
		"tool_choice":{"type":"server_tool","name":"web_search"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "server_tool") {
		t.Fatalf("expected explicit tool_choice rejection, got %v", err)
	}
}

func TestAnthropicRejectsCacheControlForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":[{"type":"text","text":"你好","cache_control":{"type":"ephemeral"}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "cache_control") {
		t.Fatalf("expected explicit cache_control rejection, got %v", err)
	}
}

func TestAnthropicRejectsUnsupportedSystemBlocksForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"system":[{"type":"text","text":"ok","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":"你好"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "cache_control") {
		t.Fatalf("expected explicit system cache_control rejection, got %v", err)
	}

	_, err = anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"system":[{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}],
		"messages":[{"role":"user","content":"你好"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected explicit unsupported system block rejection, got %v", err)
	}
}

func TestAnthropicRejectsMalformedMessagesForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":{"role":"user","content":"你好"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "messages") {
		t.Fatalf("expected explicit malformed messages rejection, got %v", err)
	}
}

func TestAnthropicRejectsMalformedContentBlocksForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":[{"type":"text","text":"你好","unknown":true}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected explicit unknown block field rejection, got %v", err)
	}

	_, err = anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","input":{}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "id and name") {
		t.Fatalf("expected explicit malformed tool_use rejection, got %v", err)
	}
}

func TestAnthropicRejectsNestedUnknownFieldsForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"system":[{"type":"text","text":"ok","unknown":true}],
		"messages":[{"role":"user","content":"你好"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "system") {
		t.Fatalf("expected explicit system unknown field rejection, got %v", err)
	}

	_, err = anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好","unknown":true}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "anthropic message") {
		t.Fatalf("expected explicit message unknown field rejection, got %v", err)
	}

	_, err = anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好"}],
		"tool_choice":"auto"
	}`))
	if err == nil || !strings.Contains(err.Error(), "tool_choice") {
		t.Fatalf("expected explicit malformed tool_choice rejection, got %v", err)
	}
}

func TestAnthropicRejectsMalformedContainerTypesForCrossFormat(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"stop_sequences":"bad",
		"messages":[{"role":"user","content":"你好"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "stop_sequences") {
		t.Fatalf("expected explicit stop_sequences type rejection, got %v", err)
	}

	_, err = anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":"你好"}],
		"tools":{"name":"lookup"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "tools") {
		t.Fatalf("expected explicit tools type rejection, got %v", err)
	}
}

func TestAnthropicToolArgumentsPreserveLargeNumberPrecision(t *testing.T) {
	ir, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"id":9007199254740993}}]}]
	}`))
	if err != nil {
		t.Fatalf("anthropicToInternal: %v", err)
	}
	if got := ir.Messages[0].ToolCalls[0].Arguments; !strings.Contains(got, "9007199254740993") {
		t.Fatalf("tool arguments lost numeric precision: %s", got)
	}
}

func TestAnthropicToolResultMapsToToolRole(t *testing.T) {
	ir, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}]
	}`))
	if err != nil {
		t.Fatalf("anthropicToInternal: %v", err)
	}
	if ir.Messages[0].Role != "tool" || ir.Messages[0].ToolResult == nil || ir.Messages[0].ToolResult.ToolCallID != "toolu_1" {
		t.Fatalf("tool_result must map to internal tool role, got %+v", ir.Messages[0])
	}
}

func TestInternalToAnthropicResponseRejectsInvalidToolArguments(t *testing.T) {
	_, err := internalToAnthropicResponse(&provider.InternalResponse{
		Choices: []provider.InternalChoice{{
			Message: provider.InternalMessage{
				ToolCalls: []provider.InternalToolCall{{ID: "toolu_1", Name: "lookup", Arguments: "{bad"}},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid tool arguments rejection, got %v", err)
	}
}

func TestInternalAssistantTextPartsJoinForAnthropic(t *testing.T) {
	out, err := internalToAnthropic(&provider.InternalRequest{
		Model: "claude-test",
		Messages: []provider.InternalMessage{{
			Role:    "assistant",
			Content: []provider.InternalContentPart{{Type: "text", Text: "hello "}, {Type: "text", Text: "world"}},
		}},
	})
	if err != nil {
		t.Fatalf("internalToAnthropic returned error: %v", err)
	}
	if !strings.Contains(string(out), `"content":"hello world"`) {
		t.Fatalf("assistant text parts should be joined without dropping later parts: %s", out)
	}
}

func TestAnthropicRejectsMultipleToolResultsInOneMessage(t *testing.T) {
	_, err := anthropicToInternal([]byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_1","content":"one"},
			{"type":"tool_result","tool_use_id":"toolu_2","content":"two"}
		]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "multiple tool_result") {
		t.Fatalf("expected multiple tool_result rejection, got %v", err)
	}
}

func TestAnthropicResponsePreservesAssistantRole(t *testing.T) {
	ir, err := anthropicResponseToInternal([]byte(`{
		"id":"msg_1",
		"model":"claude-test",
		"role":"assistant",
		"content":[{"type":"text","text":"hi"}],
		"stop_reason":"end_turn"
	}`))
	if err != nil {
		t.Fatalf("anthropicResponseToInternal: %v", err)
	}
	if ir.Choices[0].Message.Role != "assistant" {
		t.Fatalf("Anthropic response role was not preserved: %+v", ir.Choices[0].Message)
	}
}
