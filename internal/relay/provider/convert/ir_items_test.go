package convert

import (
	"encoding/json"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
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
	got, err := parseOpenAIResponsesRequestDirectIR(body)
	if err != nil {
		t.Fatalf("parseOpenAIResponsesRequestDirectIR: %v", err)
	}
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
	got, err := parseOpenAIResponsesRequestDirectIR(body)
	if err != nil {
		t.Fatalf("parseOpenAIResponsesRequestDirectIR: %v", err)
	}
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

func TestResponsesNamespaceToolsProjectToAnthropicFunctionTools(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":"use memory",
		"tools":[
			{"type":"namespace","name":"plugin:claude-mem:mcp-search","tools":[
				{"type":"function","name":"smart_search","description":"Search memory","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}
			]},
			{"type":"mcp","server_label":"claude-mem","server_url":"https://mcp.example.test","allowed_tools":["smart_search"]},
			{"type":"file_search","vector_store_ids":["vs_1"]}
		]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	tools, ok := got["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want exactly one flattened function tool; body=%s", got["tools"], converted)
	}
	tool := tools[0].(map[string]interface{})
	if tool["name"] != "plugin:claude-mem:mcp-search__smart_search" {
		t.Fatalf("flattened tool name = %#v; body=%s", tool["name"], converted)
	}
	if _, ok := tool["input_schema"].(map[string]interface{}); !ok {
		t.Fatalf("input_schema must be an object: %#v; body=%s", tool["input_schema"], converted)
	}
	for _, forbidden := range []string{"type", "server_label", "server_url", "allowed_tools", "tools", "parameters"} {
		if _, exists := tool[forbidden]; exists {
			t.Fatalf("Claude tool leaked Responses-only field %q: %#v; body=%s", forbidden, tool, converted)
		}
	}
}

func TestResponsesNamespaceToolsProjectAcrossFunctionOnlyProtocols(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":"use memory",
		"tools":[
			{"type":"namespace","name":"mcp__memory","tools":[
				{"type":"function","name":"smart_search","description":"Search memory","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}
			]},
			{"type":"mcp","server_label":"memory","server_url":"https://mcp.example.test","allowed_tools":["smart_search"]}
		],
		"tool_choice":{"type":"function","name":"smart_search","namespace":"mcp__memory"}
	}`)

	chat, err := ConvertRequest(FormatOpenAIResponses, FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("Responses -> Chat: %v", err)
	}
	chatText := string(chat)
	for _, want := range []string{`"name":"mcp__memory__smart_search"`, `"tool_choice":{"name":"mcp__memory__smart_search","type":"function"}`} {
		if indexOf(chatText, want) < 0 {
			t.Fatalf("OpenAI Chat projection missing %s:\n%s", want, chat)
		}
	}
	for _, forbidden := range []string{`"type":"mcp"`, `"server_label"`, `"server_url"`, `"allowed_tools"`} {
		if indexOf(chatText, forbidden) >= 0 {
			t.Fatalf("OpenAI Chat projection leaked %s:\n%s", forbidden, chat)
		}
	}

	gemini, err := ConvertRequest(FormatOpenAIResponses, FormatGemini, body)
	if err != nil {
		t.Fatalf("Responses -> Gemini: %v", err)
	}
	geminiText := string(gemini)
	for _, want := range []string{`"name":"mcp__memory__smart_search"`, `"allowedFunctionNames":["mcp__memory__smart_search"]`} {
		if indexOf(geminiText, want) < 0 {
			t.Fatalf("Gemini projection missing %s:\n%s", want, gemini)
		}
	}
	for _, forbidden := range []string{`"type":"mcp"`, `"server_label"`, `"server_url"`, `"allowed_tools"`} {
		if indexOf(geminiText, forbidden) >= 0 {
			t.Fatalf("Gemini projection leaked %s:\n%s", forbidden, gemini)
		}
	}
}

func TestResponsesSameProtocolPreservesMCPAndNamespaceTools(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":"use memory",
		"tools":[
			{"type":"namespace","name":"mcp__memory","tools":[{"type":"function","name":"smart_search","parameters":{"type":"object","properties":{}}}]},
			{"type":"mcp","server_label":"memory","server_url":"https://mcp.example.test","allowed_tools":["smart_search"]}
		]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("Responses -> Responses: %v", err)
	}
	text := string(converted)
	for _, want := range []string{`"type":"namespace"`, `"tools":[`, `"type":"mcp"`, `"server_label":"memory"`, `"allowed_tools":["smart_search"]`} {
		if indexOf(text, want) < 0 {
			t.Fatalf("same-protocol Responses dropped %s:\n%s", want, converted)
		}
	}
}

func TestResponsesSameProtocolNormalizesRawTextContentPartsForOpenAIWire(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"message","role":"user","content":[{"type":"text","text":"hello"}]},
			{"type":"message","role":"assistant","content":[{"type":"text","text":"hi"}]}
		]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	text := string(converted)
	for _, want := range []string{`"type":"input_text"`, `"type":"output_text"`} {
		if indexOf(text, want) < 0 {
			t.Fatalf("same-protocol Responses conversion missing %s:\n%s", want, converted)
		}
	}
	if indexOf(text, `"type":"text"`) >= 0 {
		t.Fatalf("same-protocol Responses conversion leaked invalid text content part:\n%s", converted)
	}
}

func TestResponsesSameProtocolFlattensRawFunctionCallOutputTextBlocksForOpenAIWire(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"function_call_output","call_id":"call_1","output":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}
		]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	input := got["input"].([]interface{})
	output := input[0].(map[string]interface{})
	if output["output"] != "line1\nline2" {
		t.Fatalf("function_call_output.output = %#v, want flattened string; body=%s", output["output"], converted)
	}
}

func TestResponsesSameProtocolPreservesRawFunctionCallOutputPDFFileBlock(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"function_call_output","call_id":"call_1","output":[
				{"type":"input_text","text":"PDF read successfully"},
				{"type":"input_file","filename":"paper.pdf","file_data":"data:application/pdf;base64,AA==","file_type":"application/pdf"}
			]}
		]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	input := got["input"].([]interface{})
	output := input[0].(map[string]interface{})
	blocks, ok := output["output"].([]interface{})
	if !ok {
		t.Fatalf("function_call_output.output = %#v, want structured blocks; body=%s", output["output"], converted)
	}
	if len(blocks) != 2 {
		t.Fatalf("function_call_output.output blocks = %d, want 2; body=%s", len(blocks), converted)
	}
	fileBlock := blocks[1].(map[string]interface{})
	if fileBlock["type"] != "input_file" || fileBlock["file_data"] != "data:application/pdf;base64,AA==" {
		t.Fatalf("PDF input_file block not preserved: %#v; body=%s", fileBlock, converted)
	}
}

func TestResponsesSameProtocolNormalizeRequestUsesStandardOpenAIWire(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"message","role":"user","content":[{"type":"text","text":"hello"}]},
			{"type":"message","role":"assistant","content":[{"type":"text","text":"hi"}]},
			{"type":"function_call_output","call_id":"call_1","output":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}
		]
	}`)
	normalized, err := NormalizeRequestSameProtocol(FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("NormalizeRequestSameProtocol: %v", err)
	}
	text := string(normalized)
	for _, want := range []string{`"type":"input_text"`, `"type":"output_text"`, `"output":"line1\nline2"`} {
		if indexOf(text, want) < 0 {
			t.Fatalf("same-protocol normalize missing %s:\n%s", want, normalized)
		}
	}
	if indexOf(text, `"type":"text"`) >= 0 {
		t.Fatalf("same-protocol normalize leaked invalid text content part:\n%s", normalized)
	}
}

func TestResponsesToolResultTextBlocksFlattenToStringForOpenAIWire(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":1024,
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}
		]}]
	}`)
	converted, err := ConvertRequest(FormatAnthropic, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	input := got["input"].([]interface{})
	var output map[string]interface{}
	for _, raw := range input {
		item, ok := raw.(map[string]interface{})
		if ok && item["type"] == "function_call_output" {
			output = item
			break
		}
	}
	if output == nil {
		t.Fatalf("function_call_output missing: %s", converted)
	}
	if output["output"] != "line1\nline2" {
		t.Fatalf("function_call_output.output = %#v, want flattened string; body=%s", output["output"], converted)
	}
}

func TestResponsesFunctionCallNamespaceProjectsToAnthropicToolUseName(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"output":[
			{"type":"function_call","call_id":"call_1","name":"smart_search","namespace":"plugin:claude-mem:mcp-search","arguments":"{\"query\":\"uapi\"}","status":"completed"}
		]
	}`)
	converted, err := ConvertResponse(FormatOpenAIResponses, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	text := string(converted)
	if indexOf(text, `"name":"plugin:claude-mem:mcp-search__smart_search"`) < 0 {
		t.Fatalf("Responses namespace tool call not qualified for Anthropic:\n%s", converted)
	}
}

func TestResponsesTextResponseProjectsToCleanAnthropicMessage(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"created_at":1780638631,
		"model":"gpt-5.5",
		"status":"completed",
		"output":[
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}
		],
		"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}
	}`)
	converted, err := ConvertResponse(FormatOpenAIResponses, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	for _, forbidden := range []string{"object", "created_at", "status", "output"} {
		if _, exists := got[forbidden]; exists {
			t.Fatalf("Anthropic response leaked OpenAI Responses field %q: %s", forbidden, converted)
		}
	}
	if got["type"] != "message" || got["role"] != "assistant" || got["stop_reason"] != "end_turn" {
		t.Fatalf("not a valid Anthropic message shape: %s", converted)
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
	converted, err := ConvertRequest(FormatAnthropic, FormatOpenAIResponses, body)
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
	converted, err := ConvertRequest(FormatGemini, FormatGemini, body)
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

func TestGeminiFunctionResponseExtendedFieldsRecordCrossProtocolLoss(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"functionResponse":{
			"name":"lookup",
			"id":"fr_1",
			"response":{"content":[{"text":"tool result"}]},
			"willContinue":true,
			"scheduling":"SILENT",
			"parts":[{"text":"nested"}],
			"vendorField":{"x":1}
		}}]}]
	}`)
	converted, audit, err := ConvertRequestDetailed(FormatGemini, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequestDetailed: %v", err)
	}
	if indexOf(string(converted), `"function_call_output"`) < 0 {
		t.Fatalf("converted body should contain tool result output: %s", converted)
	}
	for _, field := range []string{"response", "id", "willContinue", "scheduling", "parts", "vendorField"} {
		if !hasLossField(audit.Losses, field) {
			t.Fatalf("missing functionResponse loss for %s: %#v", field, audit.Losses)
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
	converted, err := FromIR(req, FormatOpenAIResponses)
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

func TestProtocolResponseViewToIRPreservesUsageAndOrderedItems(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_1",
		"model":"gpt-test",
		"choices":[{
			"index":0,
			"message":{
				"role":"assistant",
				"content":"answer",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]
			},
			"finish_reason":"tool_calls"
		}],
		"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2}}
	}`)
	got, err := parseOpenAIChatResponseDirectIR(body)
	if err != nil {
		t.Fatalf("parseOpenAIChatResponseDirectIR: %v", err)
	}
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

func hasLossField(losses []ir.Loss, field string) bool {
	for _, loss := range losses {
		if loss.Field == field && loss.ValueHash != "" && loss.Preserved {
			return true
		}
	}
	return false
}
