package convert

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAnthropicRequestAcceptsStringContent(t *testing.T) {
	body := []byte(`{"model":"claude-test","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	got, err := parseAnthropicRequestDirectIR(body)
	if err != nil {
		t.Fatalf("parseAnthropicRequestDirectIR() error = %v", err)
	}
	if len(got.Turns) != 1 || len(got.Turns[0].Items) != 1 {
		t.Fatalf("turns = %#v", got.Turns)
	}
	item := got.Turns[0].Items[0]
	if item.Text == nil || item.Text.Text != "hi" {
		t.Fatalf("content item = %#v", item)
	}
}

func TestParseAnthropicToolResultAcceptsContentBlocks(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}]}]
	}`)
	got, err := parseAnthropicRequestDirectIR(body)
	if err != nil {
		t.Fatalf("parseAnthropicRequestDirectIR() error = %v", err)
	}
	if len(got.Turns) != 1 || len(got.Turns[0].Items) != 1 {
		t.Fatalf("turns = %#v", got.Turns)
	}
	item := got.Turns[0].Items[0]
	if item.ToolResult == nil {
		t.Fatalf("item kind = %q, want tool_result; item=%#v", item.Kind, item)
	}
	if item.ToolResult.CallID != "toolu_1" || item.ToolResult.OutputText != "onetwo" {
		t.Fatalf("tool result = %#v", item.ToolResult)
	}
}

func TestParseAnthropicImageURLSource(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}]}]
	}`)
	got, err := parseAnthropicRequestDirectIR(body)
	if err != nil {
		t.Fatalf("parseAnthropicRequestDirectIR() error = %v", err)
	}
	if len(got.Turns) != 1 || len(got.Turns[0].Items) != 1 {
		t.Fatalf("turns = %#v", got.Turns)
	}
	item := got.Turns[0].Items[0]
	if item.Image == nil || item.Image.URL != "https://example.com/image.png" {
		t.Fatalf("image URL source not preserved: %#v", item)
	}
}

func TestAnthropicMissingMaxTokensDoesNotEmitZero(t *testing.T) {
	body := []byte(`{"model":"claude-test","messages":[{"role":"user","content":"hi"}]}`)
	converted, err := ConvertRequest(FormatAnthropic, FormatClaudeCode, body)
	if err != nil {
		t.Fatalf("Anthropic -> ClaudeCode: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if got["max_tokens"] == float64(0) {
		t.Fatalf("missing max_tokens must not be rewritten as zero: %s", converted)
	}
}

func TestAnthropicMetadataPreserved(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"metadata":{"user_id":"user_1"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	converted, err := ConvertRequest(FormatAnthropic, FormatClaudeCode, body)
	if err != nil {
		t.Fatalf("Anthropic -> ClaudeCode: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	metadata := got["metadata"].(map[string]interface{})
	if metadata["user_id"] != "user_1" {
		t.Fatalf("metadata not preserved: %#v; body=%s", metadata, converted)
	}
}

func TestAnthropicToolWithoutTypeDoesNotBecomeOpaqueForClaudeCode(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}]
	}`)
	converted, err := ConvertRequest(FormatAnthropic, FormatClaudeCode, body)
	if err != nil {
		t.Fatalf("Anthropic -> ClaudeCode: %v", err)
	}
	var got struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	if got.Tools[0]["type"] != nil {
		t.Fatalf("Anthropic custom tool must not emit unsupported type: %#v; body=%s", got.Tools[0]["type"], converted)
	}
	if got.Tools[0]["name"] != "Read" || got.Tools[0]["input_schema"] == nil {
		t.Fatalf("tool fields not preserved: %#v; body=%s", got.Tools[0], converted)
	}
}

func TestAnthropicSkillToolToOpenAIResponsesUsesParameters(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"name":"Skill","description":"Execute skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"},"args":{"type":"string"}},"required":["skill"]}}]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Responses: %v", err)
	}
	var got struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	tool := got.Tools[0]
	if tool["type"] != "function" || tool["name"] != "Skill" {
		t.Fatalf("tool not normalized as OpenAI Responses function: %#v; body=%s", tool, converted)
	}
	if tool["input_schema"] != nil {
		t.Fatalf("OpenAI Responses tool must not expose Anthropic input_schema: %#v; body=%s", tool, converted)
	}
	params, ok := tool["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("parameters missing: %#v; body=%s", tool, converted)
	}
	required, ok := params["required"].([]interface{})
	if !ok || len(required) != 1 || required[0] != "skill" {
		t.Fatalf("required skill not preserved: %#v; body=%s", params, converted)
	}
}

func TestClaudeCodeAgentToolRequiredSchemaSurvivesResponsesConversion(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"delegate"}],
		"tools":[{
			"name":"Agent",
			"description":"Launch a subagent",
			"input_schema":{
				"type":"object",
				"properties":{
					"description":{"type":"string"},
					"prompt":{"type":"string"},
					"subagent_type":{"type":"string"}
				},
				"required":["description","prompt"]
			}
		}]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Responses: %v", err)
	}
	var got struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	params, ok := got.Tools[0]["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("Agent parameters missing: %#v; body=%s", got.Tools[0], converted)
	}
	required, ok := params["required"].([]interface{})
	if !ok || len(required) != 2 || required[0] != "description" || required[1] != "prompt" {
		t.Fatalf("Agent required parameters not preserved: %#v; body=%s", params["required"], converted)
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok || props["description"] == nil || props["prompt"] == nil {
		t.Fatalf("Agent description/prompt properties not preserved: %#v; body=%s", props, converted)
	}
}

func TestAnthropicSkillToolToOpenAIChatUsesFunctionParameters(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"name":"Skill","description":"Execute skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"},"args":{"type":"string"}},"required":["skill"]}}]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Chat: %v", err)
	}
	var got struct {
		Tools             []map[string]interface{} `json:"tools"`
		ToolChoice        string                   `json:"tool_choice"`
		ParallelToolCalls *bool                    `json:"parallel_tool_calls"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	if got.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %q, want auto; body=%s", got.ToolChoice, converted)
	}
	if got.ParallelToolCalls == nil || !*got.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls = %#v, want true; body=%s", got.ParallelToolCalls, converted)
	}
	tool := got.Tools[0]
	if tool["type"] != "function" {
		t.Fatalf("tool not normalized as OpenAI Chat function: %#v; body=%s", tool, converted)
	}
	function, ok := tool["function"].(map[string]interface{})
	if !ok || function["name"] != "Skill" {
		t.Fatalf("function missing: %#v; body=%s", tool, converted)
	}
	if tool["input_schema"] != nil {
		t.Fatalf("OpenAI Chat tool must not expose Anthropic input_schema: %#v; body=%s", tool, converted)
	}
	params, ok := function["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("function parameters missing: %#v; body=%s", function, converted)
	}
	required, ok := params["required"].([]interface{})
	if !ok || len(required) != 1 || required[0] != "skill" {
		t.Fatalf("required skill not preserved: %#v; body=%s", params, converted)
	}
}

func TestAnthropicSkillToolToOpenAIResponsesUsesExplicitToolDefaults(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"name":"Skill","description":"Execute skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"},"args":{"type":"string"}},"required":["skill"]}}]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIResponses, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Responses: %v", err)
	}
	var got struct {
		Tools             []map[string]interface{} `json:"tools"`
		ToolChoice        string                   `json:"tool_choice"`
		ParallelToolCalls *bool                    `json:"parallel_tool_calls"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	if got.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %q, want auto; body=%s", got.ToolChoice, converted)
	}
	if got.ParallelToolCalls == nil || !*got.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls = %#v, want true; body=%s", got.ParallelToolCalls, converted)
	}
}

func TestClaudeCodeToolResultToOpenAIChatUsesToolRole(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Skill","input":{"skill":"brainstorming"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"Launching skill: brainstorming"}]}
		],
		"tools":[{"name":"Skill","description":"Execute skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"}},"required":["skill"]}}]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Chat: %v", err)
	}
	var got struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %#v; body=%s", got.Messages, converted)
	}
	if got.Messages[0].Role != "assistant" || len(got.Messages[0].ToolCalls) != 1 || got.Messages[0].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant tool call not preserved: %#v; body=%s", got.Messages[0], converted)
	}
	if got.Messages[1].Role != "tool" || got.Messages[1].ToolCallID != "call_1" {
		t.Fatalf("tool result must be OpenAI Chat tool message: %#v; body=%s", got.Messages[1], converted)
	}
}

func TestClaudeCodeMultipleToolResultsToOpenAIChatUseSeparateToolMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[
			{"role":"assistant","content":[
				{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"git status --short"}},
				{"type":"tool_use","id":"call_2","name":"TaskList","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_2","content":"No tasks found"},
				{"type":"tool_result","tool_use_id":"call_1","content":" M file.go"}
			]}
		],
		"tools":[
			{"name":"Bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}},
			{"name":"TaskList","input_schema":{"type":"object","properties":{}}}
		]
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Chat: %v", err)
	}
	var got struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %#v; body=%s", got.Messages, converted)
	}
	if got.Messages[1].Role != "tool" || got.Messages[1].ToolCallID != "call_2" {
		t.Fatalf("first tool result not emitted separately: %#v; body=%s", got.Messages[1], converted)
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("second tool result not emitted separately: %#v; body=%s", got.Messages[2], converted)
	}
}

func TestClaudeCodeToOpenAIChatDropsNativeAnthropicFields(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}],
		"thinking":{"type":"adaptive"},
		"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},
		"output_config":{"effort":"medium"},
		"metadata":{"trace":"abc"}
	}`)
	converted, err := ConvertRequest(FormatClaudeCode, FormatOpenAIChatCompletions, body)
	if err != nil {
		t.Fatalf("ClaudeCode -> OpenAI Chat: %v", err)
	}
	for _, forbidden := range []string{
		`"thinking"`,
		`"context_management"`,
		`"output_config"`,
		`"metadata"`,
		`"cache_control"`,
	} {
		if strings.Contains(string(converted), forbidden) {
			t.Fatalf("converted Chat request leaked %s:\n%s", forbidden, converted)
		}
	}
}

func TestOpenAIFunctionToolToAnthropicOmitsFunctionType(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}]
	}`)
	converted, err := ConvertRequest(FormatOpenAIChatCompletions, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Chat -> Anthropic: %v", err)
	}
	var got struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	if got.Tools[0]["type"] != nil {
		t.Fatalf("Anthropic function tool must not emit OpenAI type: %#v; body=%s", got.Tools[0]["type"], converted)
	}
	if got.Tools[0]["name"] != "lookup" || got.Tools[0]["input_schema"] == nil {
		t.Fatalf("function tool fields not preserved: %#v; body=%s", got.Tools[0], converted)
	}
}

func TestOpenAIResponsesWebSearchToolToAnthropicBuiltin(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":"hi",
		"tools":[{"type":"web_search","name":"web_search","max_uses":2,"filters":{"allowed_domains":["example.com"]},"user_location":{"type":"approximate","country":"US"}}]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Responses -> Anthropic: %v", err)
	}
	var got struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %#v; body=%s", got.Tools, converted)
	}
	tool := got.Tools[0]
	if tool["type"] != "web_search_20250305" || tool["name"] != "web_search" {
		t.Fatalf("web search tool not converted to Claude builtin: %#v; body=%s", tool, converted)
	}
	if tool["max_uses"] != float64(2) || tool["user_location"] == nil || tool["allowed_domains"] == nil {
		t.Fatalf("web search tool fields not preserved/mapped: %#v; body=%s", tool, converted)
	}
	if _, ok := tool["filters"]; ok {
		t.Fatalf("OpenAI filters object leaked instead of Claude allowed_domains: %#v; body=%s", tool, converted)
	}
}

func TestOpenAIResponsesWebSearchDisabledOmittedForAnthropic(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":"hi",
		"tools":[{"type":"web_search","external_web_access":false}]
	}`)
	converted, err := ConvertRequest(FormatOpenAIResponses, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Responses -> Anthropic: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("disabled web search tool must be omitted: %s", converted)
	}
}

func TestAnthropicResponseNestedCacheCreationUsage(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4},"server_tool_use":{"web_search_requests":1}}
	}`)
	converted, err := ConvertResponse(FormatAnthropic, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("Anthropic -> Anthropic response: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	usage := got["usage"].(map[string]interface{})
	if usage["cache_creation_input_tokens"] != float64(7) {
		t.Fatalf("nested cache_creation not totaled: %#v; body=%s", usage, converted)
	}
	if usage["cache_creation"] == nil || usage["server_tool_use"] == nil {
		t.Fatalf("raw Claude usage extras not preserved: %#v; body=%s", usage, converted)
	}
}

func TestAnthropicResponseContentBlockExtrasPreserved(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[
			{"type":"text","text":"ok","citations":[{"type":"char_location","start_char_index":0,"end_char_index":2}],"cache_control":{"type":"ephemeral"}},
			{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"uapi"}}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":2}
	}`)
	converted, err := ConvertResponse(FormatAnthropic, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("Anthropic -> Anthropic response: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	content := got["content"].([]interface{})
	text := content[0].(map[string]interface{})
	if text["citations"] == nil || text["cache_control"] == nil {
		t.Fatalf("text content block extras not preserved: %#v; body=%s", text, converted)
	}
	unknown := content[1].(map[string]interface{})
	if unknown["type"] != "server_tool_use" || unknown["id"] != "srv_1" || unknown["input"] == nil {
		t.Fatalf("unknown Claude content block not preserved: %#v; body=%s", unknown, converted)
	}
}

func TestOpenAIToolChoiceRequiredToAnthropicDisablesThinking(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}],
		"tool_choice":"required",
		"reasoning_effort":"high"
	}`)
	converted, err := ConvertRequest(FormatOpenAIChatCompletions, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Chat -> Anthropic: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	toolChoice := got["tool_choice"].(map[string]interface{})
	if toolChoice["type"] != "any" {
		t.Fatalf("tool_choice = %#v, want type any; body=%s", toolChoice, converted)
	}
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking must be omitted when Claude tool_choice forces tool use: %s", converted)
	}
}

func TestOpenAIFunctionToolChoiceToAnthropicToolChoice(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}],
		"tool_choice":{"type":"function","function":{"name":"lookup"}}
	}`)
	converted, err := ConvertRequest(FormatOpenAIChatCompletions, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Chat -> Anthropic: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	toolChoice := got["tool_choice"].(map[string]interface{})
	if toolChoice["type"] != "tool" || toolChoice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want Claude specific tool choice; body=%s", toolChoice, converted)
	}
	if _, ok := toolChoice["function"]; ok {
		t.Fatalf("OpenAI function object leaked into Anthropic tool_choice: %#v; body=%s", toolChoice, converted)
	}
}

func TestAnthropicToolChoiceParallelMapping(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}],
		"tool_choice":"auto",
		"parallel_tool_calls":false
	}`)
	converted, err := ConvertRequest(FormatOpenAIChatCompletions, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Chat -> Anthropic: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	toolChoice := got["tool_choice"].(map[string]interface{})
	if toolChoice["type"] != "auto" || toolChoice["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice = %#v, want auto with disable_parallel_tool_use=true; body=%s", toolChoice, converted)
	}
}

func TestAnthropicToolChoiceNoneDropsParallelField(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":"none",
		"parallel_tool_calls":false
	}`)
	converted, err := ConvertRequest(FormatOpenAIChatCompletions, FormatAnthropic, body)
	if err != nil {
		t.Fatalf("OpenAI Chat -> Anthropic: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	toolChoice := got["tool_choice"].(map[string]interface{})
	if toolChoice["type"] != "none" {
		t.Fatalf("tool_choice = %#v, want none; body=%s", toolChoice, converted)
	}
	if _, ok := toolChoice["disable_parallel_tool_use"]; ok {
		t.Fatalf("tool_choice none must not carry disable_parallel_tool_use: %#v; body=%s", toolChoice, converted)
	}
}

func TestAnthropicForcedToolChoiceDropsThinkingAndOutputEffort(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":8,
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":{"type":"any"},
		"thinking":{"type":"enabled","budget_tokens":1024},
		"output_config":{"effort":"max"}
	}`)
	converted, err := ConvertRequest(FormatAnthropic, FormatClaudeCode, body)
	if err != nil {
		t.Fatalf("Anthropic -> ClaudeCode: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking must be omitted when Claude tool_choice forces tool use: %s", converted)
	}
	if _, ok := got["output_config"]; ok {
		t.Fatalf("empty output_config must be omitted after effort removal: %s", converted)
	}
}
