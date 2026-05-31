package convert_test

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func TestOpenAIResponsesToIRPreservesOrderedItemsAndNativeRaw(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"id":"msg_1","type":"message","role":"user","status":"completed","content":[
				{"type":"input_text","text":"read this"},
				{"type":"input_file","file_data":"data:text/plain;base64,Zm9v","filename":"note.txt","file_type":"text/plain"}
			]},
			{"id":"call_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"uapi\"}"},
			{"id":"out_1","type":"function_call_output","call_id":"call_1","output":"{\"ok\":true}"}
		],
		"parallel_tool_calls":false,
		"store":false
	}`)
	req, err := convert.ParseOpenAIResponsesRequest(body)
	if err != nil {
		t.Fatalf("ParseOpenAIResponsesRequest: %v", err)
	}
	got := req.ToIR()
	if got.Native.Protocol != ir.ProtocolOpenAIResponses {
		t.Fatalf("protocol = %q, want %q", got.Native.Protocol, ir.ProtocolOpenAIResponses)
	}
	if got.SourceProtocol != ir.ProtocolOpenAIResponses || len(got.Native.RawBody) == 0 {
		t.Fatalf("source/raw body not preserved: source=%q raw=%d", got.SourceProtocol, len(got.Native.RawBody))
	}
	if got.Generation.ParallelToolCalls == nil || *got.Generation.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls explicit false not represented: %#v", got.Generation.ParallelToolCalls)
	}
	if got.Generation.Store == nil || *got.Generation.Store {
		t.Fatalf("store explicit false not represented: %#v", got.Generation.Store)
	}
	if len(got.Losses) != 0 {
		t.Fatalf("known loss recorded for fully represented fixture: %#v", got.Losses)
	}
	if len(got.Turns) != 3 {
		t.Fatalf("turns = %d, want 3: %#v", len(got.Turns), got.Turns)
	}
	first := got.Turns[0]
	if first.ID != "msg_1" || first.Status != "completed" {
		t.Fatalf("turn metadata not preserved: %#v", first)
	}
	if len(first.Native.Raw) == 0 {
		t.Fatalf("native raw item missing")
	}
	if len(first.Items) != 2 || first.Items[0].Kind != ir.ItemText || first.Items[1].Kind != ir.ItemFile {
		t.Fatalf("message items not ordered text,file: %#v", first.Items)
	}
	if first.Items[0].OriginalIndex != 0 || first.Items[1].OriginalIndex != 1 {
		t.Fatalf("original indexes not preserved: %#v", first.Items)
	}
	if got.Turns[1].Items[0].Kind != ir.ItemToolUse || got.Turns[2].Items[0].Kind != ir.ItemToolResult {
		t.Fatalf("tool use/result not represented as ordered IR items: %#v", got.Turns)
	}
}

func TestResponsesOpaqueItemRecordsAuditLoss(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[{"id":"fs_1","type":"file_search_call","status":"completed","queries":["uapi"]}]
	}`)
	req, err := convert.ParseOpenAIResponsesRequest(body)
	if err != nil {
		t.Fatalf("ParseOpenAIResponsesRequest: %v", err)
	}
	got := req.ToIR()
	if len(got.Losses) != 1 {
		t.Fatalf("losses = %d, want 1: %#v", len(got.Losses), got.Losses)
	}
	if got.Losses[0].Field != "file_search_call" {
		t.Fatalf("loss field = %q, want file_search_call", got.Losses[0].Field)
	}
	if got.Losses[0].ValueHash == "" || !got.Losses[0].Preserved {
		t.Fatalf("loss should include hash and preserved marker: %#v", got.Losses[0])
	}
	if len(got.Turns) != 1 || len(got.Turns[0].Items) != 1 || got.Turns[0].Items[0].Kind != ir.ItemOpaque {
		t.Fatalf("opaque item not preserved in IR: %#v", got.Turns)
	}
}

func TestOrderedPartsDriveResponsesEmission(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":1024,
		"messages":[{"role":"assistant","content":[
			{"type":"thinking","thinking":"plan","signature":"sig"},
			{"type":"text","text":"answer"},
			{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"uapi"}}
		]}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatAnthropic, convert.FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	text := string(converted)
	reasoningIdx := indexOrFatal(t, text, `"type":"reasoning"`)
	messageIdx := indexOrFatal(t, text, `"type":"message"`)
	callIdx := indexOrFatal(t, text, `"type":"function_call"`)
	if !(reasoningIdx < messageIdx && messageIdx < callIdx) {
		t.Fatalf("Responses input order changed, want reasoning -> message -> function_call:\n%s", converted)
	}
}

func TestGeminiFunctionResponseNativeFieldsSurviveSameFormat(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"functionResponse":{
			"name":"lookup",
			"id":"fr_1",
			"response":{"ok":true},
			"willContinue":true,
			"scheduling":"SILENT",
			"parts":[{"text":"nested"}],
			"vendorField":{"x":1}
		}}]}]
	}`)
	converted, err := convert.ConvertRequest(convert.FormatGemini, convert.FormatGemini, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	text := string(converted)
	for _, want := range []string{
		`"id":"fr_1"`,
		`"willContinue":true`,
		`"scheduling":"SILENT"`,
		`"parts":[{"text":"nested"}]`,
		`"vendorField":{"x":1}`,
	} {
		if indexOf(text, want) < 0 {
			t.Fatalf("converted Gemini functionResponse dropped %s:\n%s", want, converted)
		}
	}
}

func TestIRToOpenAIResponsesUsesOrderedItems(t *testing.T) {
	req := &ir.Request{
		SourceProtocol: ir.ProtocolOpenAIResponses,
		TargetProtocol: ir.ProtocolOpenAIResponses,
		Model:          "gpt-5",
		Turns: []ir.Turn{{
			Role: ir.RoleAssistant,
			Items: []ir.Item{
				{Kind: ir.ItemReasoning, Reasoning: &ir.Reasoning{Text: "plan"}},
				{Kind: ir.ItemText, Text: &ir.Text{Text: "answer"}},
				{Kind: ir.ItemToolUse, CallID: "call_1", Name: "lookup", ToolUse: &ir.ToolUse{CallID: "call_1", Name: "lookup", ArgumentsText: `{"q":"uapi"}`}},
			},
		}},
	}
	converted, err := convert.FromIR(req, convert.FormatOpenAIResponses)
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	text := string(converted)
	reasoningIdx := indexOrFatal(t, text, `"type":"reasoning"`)
	messageIdx := indexOrFatal(t, text, `"type":"message"`)
	callIdx := indexOrFatal(t, text, `"type":"function_call"`)
	if !(reasoningIdx < messageIdx && messageIdx < callIdx) {
		t.Fatalf("IR conversion did not preserve item order:\n%s", converted)
	}
}

func TestInternalResponseToIRPreservesUsageAndOrderedItems(t *testing.T) {
	resp := &convert.InternalResponse{
		ID:    "chatcmpl_1",
		Model: "gpt-test",
		Usage: schema.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, CacheReadInputTokens: 2},
		Choices: []convert.InternalChoice{{
			Index: 0,
			Role:  "assistant",
			Items: []convert.ContentItem{
				{Kind: "content", Content: schema.ContentPart{Type: "text", Text: "answer"}},
				{Kind: "tool_call", ToolCall: schema.ToolCall{ID: "call_1", Type: "function", Name: "lookup"}},
			},
			FinishReason: "tool_calls",
		}},
	}
	got := resp.ToIR(convert.FormatOpenAIChatCompletions)
	if got.SourceProtocol != ir.ProtocolOpenAIChat || got.Usage == nil || got.Usage.CacheReadTokens != 2 {
		t.Fatalf("response IR metadata/usage not preserved: %#v", got)
	}
	if len(got.Choices) != 1 || len(got.Choices[0].Items) != 2 {
		t.Fatalf("response IR items missing: %#v", got.Choices)
	}
	if got.Choices[0].Items[0].Kind != ir.ItemText || got.Choices[0].Items[1].Kind != ir.ItemToolUse {
		t.Fatalf("response IR item order wrong: %#v", got.Choices[0].Items)
	}
	if got.Choices[0].Finish == nil || got.Choices[0].Finish.Reason != ir.FinishToolCall {
		t.Fatalf("finish reason not normalized: %#v", got.Choices[0].Finish)
	}
}

func indexOrFatal(t *testing.T, text, needle string) int {
	t.Helper()
	if idx := indexOf(text, needle); idx >= 0 {
		return idx
	}
	t.Fatalf("missing %s in %s", needle, text)
	return -1
}

func indexOf(text, needle string) int {
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
