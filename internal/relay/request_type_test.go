package relay

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/valyala/fasthttp"
)

func TestDetectRelayRequestTypeCoversProtocolFamilies(t *testing.T) {
	cases := map[string]relayRequestType{
		"/v1/chat/completions":                  requestTypeChatCompletion,
		"/v1/responses":                         requestTypeResponses,
		"/v1/messages":                          requestTypeMessages,
		"/v1beta/models/gemini:generateContent": requestTypeGeminiGenerate,
		"/v1/images/generations":                requestTypeImageGeneration,
		"/v1/images/edits":                      requestTypeImageEdit,
		"/v1/audio/speech":                      requestTypeSpeech,
		"/v1/audio/transcriptions":              requestTypeTranscription,
		"/v1/embeddings":                        requestTypeEmbedding,
		"/v1/moderations":                       requestTypeModeration,
		"/v1/realtime/sessions":                 requestTypeRealtime,
		"/v1/videos":                            requestTypeVideoGeneration,
	}
	for path, want := range cases {
		if got := detectRelayRequestType(path); got != want {
			t.Fatalf("detectRelayRequestType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDetectRelayRequestTypeRejectsUnsupportedConversionRoutes(t *testing.T) {
	paths := []string{
		"/v1/responses/input_tokens",
		"/v1/messages/count_tokens",
		"/v1/messages/batches",
		"/v1/messages/raw",
		"/v1/async/responses",
		"/v1/async/chat/completions",
		"/v1/chat/completions/extra",
		"/openai/v1/chat/completions",
		"/openai/chat/completions",
		"/openai/deployments/dep/chat/completions",
		"/v1/batches",
		"/v1/files",
		"/v1/files/file_1",
		"/v1/containers",
		"/v1beta/models/gemini:countTokens",
		"/v1beta/models/gemini:embedContent",
		"/v1beta/models/gemini:batchEmbedContents",
		"/v1beta/models/imagen:predict",
		"/v1beta/models/veo:predictLongRunning",
		"/v1beta/models/gemini:batchGenerateContent",
		"/v1beta/files",
		"/v1beta/cachedContents",
		"/v1beta/batches",
		"/v1beta/operations/batch_1",
	}
	for _, path := range paths {
		if got := detectRelayRequestType(path); got != requestTypeUnsupported {
			t.Fatalf("detectRelayRequestType(%q) = %q, want %q", path, got, requestTypeUnsupported)
		}
	}
}

func TestHandleRelayRejectsUnsupportedRoutesBeforeAuthOrAdaptor(t *testing.T) {
	paths := []string{
		"/v1/responses/input_tokens",
		"/v1/messages/count_tokens",
		"/v1beta/models/gemini:countTokens",
		"/v1/files",
	}
	for _, path := range paths {
		var req fasthttp.Request
		req.Header.SetMethod(fasthttp.MethodPost)
		req.SetRequestURI(path)
		var ctx fasthttp.RequestCtx
		ctx.Init(&req, nil, nil)

		(&Relayer{}).HandleRelay(&ctx)

		if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
			t.Fatalf("HandleRelay(%q) status = %d, want 400", path, ctx.Response.StatusCode())
		}
		if got := string(ctx.Response.Body()); got != `{"error":"unsupported route"}` {
			t.Fatalf("HandleRelay(%q) body = %q", path, got)
		}
	}
}

func TestSupportsRelayRequestTypeMakesNonTextCapabilitiesExplicit(t *testing.T) {
	if !supportsRelayRequestType("antigravity", requestTypeImageGeneration) {
		t.Fatalf("antigravity should support image generation")
	}
	if !supportsRelayRequestType("antigravity", requestTypeImageEdit) {
		t.Fatalf("antigravity should support image edit via image_gen reference images")
	}
	if !supportsRelayRequestType("openai", requestTypeSpeech) {
		t.Fatalf("openai should passthrough speech")
	}
	if supportsRelayRequestType("gemini", requestTypeEmbedding) {
		t.Fatalf("gemini embeddings should remain unsupported until a converter exists")
	}
	if supportsRelayRequestType("antigravity", requestTypeRealtime) {
		t.Fatalf("antigravity realtime passthrough should remain unsupported")
	}
	if !supportsRelayRequestType("openai", requestTypeVideoGeneration) {
		t.Fatalf("openai should passthrough video generation")
	}
}

func TestNormalizeCodexResponsesRequestAddsRequiredDefaults(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","store":true,"max_output_tokens":1,"temperature":0.2,"top_k":10,"service_tier":"auto","user":"u1","prompt_cache_key":"thread-1","tools":[{"type":"web_search_preview"}]}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	if body["instructions"] != "" {
		t.Fatalf("instructions = %#v, want empty string", body["instructions"])
	}
	if body["store"] != false {
		t.Fatalf("store = %#v, want false", body["store"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %#v, want true", body["stream"])
	}
	if body["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", body["parallel_tool_calls"])
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %#v, want auto", body["tool_choice"])
	}
	if body["prompt_cache_key"] != "thread-1" {
		t.Fatalf("prompt_cache_key = %#v, want thread-1", body["prompt_cache_key"])
	}
	if _, ok := body["include"]; ok {
		t.Fatalf("include should be omitted when reasoning is absent: %#v", body["include"])
	}
	for _, key := range []string{"max_output_tokens", "temperature", "top_k", "service_tier", "user"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s should be removed for codex: %#v", key, body)
		}
	}
	tools, ok := body["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", body["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok || tool["type"] != "web_search" {
		t.Fatalf("tool = %#v, want web_search", tools[0])
	}
	input, ok := body["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one message item", body["input"])
	}
	first, ok := input[0].(map[string]interface{})
	if !ok || first["type"] != "message" || first["role"] != "user" {
		t.Fatalf("input[0] = %#v, want user message item", input[0])
	}
	content, ok := first["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want input_text part", first["content"])
	}
	part, ok := content[0].(map[string]interface{})
	if !ok || part["type"] != "input_text" || part["text"] != "hi" {
		t.Fatalf("content[0] = %#v, want input_text hi", content[0])
	}
}

func TestNormalizeCodexResponsesRequestAddsStableClientMetadata(t *testing.T) {
	first := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","prompt_cache_key":"thread-1"}`), true, "channel-a:account-a")
	second := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","prompt_cache_key":"thread-1"}`), true, "channel-a:account-a")

	var firstBody, secondBody map[string]interface{}
	if err := json.Unmarshal(first, &firstBody); err != nil {
		t.Fatalf("first normalized body is not JSON: %v", err)
	}
	if err := json.Unmarshal(second, &secondBody); err != nil {
		t.Fatalf("second normalized body is not JSON: %v", err)
	}
	firstMetadata, ok := firstBody["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("first client_metadata missing: %#v", firstBody)
	}
	secondMetadata, ok := secondBody["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("second client_metadata missing: %#v", secondBody)
	}
	installationID, _ := firstMetadata["x-codex-installation-id"].(string)
	if installationID == "" {
		t.Fatalf("x-codex-installation-id missing: %#v", firstMetadata)
	}
	if secondMetadata["x-codex-installation-id"] != installationID {
		t.Fatalf("x-codex-installation-id is not stable: first=%#v second=%#v", firstMetadata, secondMetadata)
	}
	if firstMetadata["x-codex-window-id"] != "thread-1:0" {
		t.Fatalf("x-codex-window-id = %#v, want thread-1:0", firstMetadata["x-codex-window-id"])
	}
	if firstMetadata["x-codex-turn-metadata"] == nil {
		t.Fatalf("x-codex-turn-metadata should be synthesized: %#v", firstMetadata)
	}
	if firstBody["prompt_cache_key"] != "thread-1" {
		t.Fatalf("prompt_cache_key should be preserved: %#v", firstBody["prompt_cache_key"])
	}
}

func TestNormalizeCodexResponsesRequestAddsStablePromptCacheKeyWhenMissing(t *testing.T) {
	first := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi"}`), true, "channel-a:account-a")
	second := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi again"}`), true, "channel-a:account-a")

	var firstBody, secondBody map[string]interface{}
	if err := json.Unmarshal(first, &firstBody); err != nil {
		t.Fatalf("first normalized body is not JSON: %v", err)
	}
	if err := json.Unmarshal(second, &secondBody); err != nil {
		t.Fatalf("second normalized body is not JSON: %v", err)
	}
	firstKey, _ := firstBody["prompt_cache_key"].(string)
	if firstKey == "" {
		t.Fatalf("prompt_cache_key missing: %#v", firstBody)
	}
	if secondBody["prompt_cache_key"] != firstKey {
		t.Fatalf("prompt_cache_key is not stable: first=%#v second=%#v", firstBody["prompt_cache_key"], secondBody["prompt_cache_key"])
	}
}

func TestNormalizeCodexResponsesRequestPromptCacheKeyIsScoped(t *testing.T) {
	first := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi"}`), true, "channel-a:account-a:scope-a")
	second := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi"}`), true, "channel-a:account-a:scope-b")

	var firstBody, secondBody map[string]interface{}
	if err := json.Unmarshal(first, &firstBody); err != nil {
		t.Fatalf("first normalized body is not JSON: %v", err)
	}
	if err := json.Unmarshal(second, &secondBody); err != nil {
		t.Fatalf("second normalized body is not JSON: %v", err)
	}
	if firstBody["prompt_cache_key"] == secondBody["prompt_cache_key"] {
		t.Fatalf("prompt_cache_key should differ across scopes: %#v", firstBody["prompt_cache_key"])
	}
}

func TestNormalizeCodexResponsesRequestAlignsExistingClientMetadata(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":"hi",
		"prompt_cache_key":"thread-1",
		"client_metadata":{
			"x-codex-installation-id":"install-existing",
			"x-codex-window-id":"window-existing",
			"x-codex-turn-metadata":"{\"prompt_cache_key\":\"old-thread\",\"turn_id\":\"turn-existing\",\"window_id\":\"old-thread:0\"}",
			"custom":"keep"
		}
	}`), true, "channel-a:account-a")

	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	metadata, ok := body["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("client_metadata missing: %#v", body)
	}
	if metadata["x-codex-installation-id"] != "install-existing" {
		t.Fatalf("installation id overwritten: %#v", metadata)
	}
	if metadata["x-codex-window-id"] != "thread-1:0" {
		t.Fatalf("window id not aligned: %#v", metadata)
	}
	rawTurnMetadata, _ := metadata["x-codex-turn-metadata"].(string)
	var turnMetadata map[string]string
	if err := json.Unmarshal([]byte(rawTurnMetadata), &turnMetadata); err != nil {
		t.Fatalf("turn metadata is not JSON: %v; %q", err, rawTurnMetadata)
	}
	if turnMetadata["prompt_cache_key"] != "thread-1" || turnMetadata["window_id"] != "thread-1:0" || turnMetadata["turn_id"] != "turn-existing" {
		t.Fatalf("turn metadata not aligned: %#v", turnMetadata)
	}
	if metadata["custom"] != "keep" {
		t.Fatalf("custom metadata lost: %#v", metadata)
	}
}

func TestNormalizeCodexResponsesRequestAddsCacheIdentityMetadata(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","prompt_cache_key":"thread-1"}`), true, "channel-a:account-a")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	metadata, ok := body["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("client_metadata missing: %#v", body)
	}
	if metadata["x-codex-window-id"] != "thread-1:0" {
		t.Fatalf("x-codex-window-id = %#v, want thread-1:0", metadata["x-codex-window-id"])
	}
	rawTurnMetadata, _ := metadata["x-codex-turn-metadata"].(string)
	if rawTurnMetadata == "" {
		t.Fatalf("x-codex-turn-metadata missing: %#v", metadata)
	}
	var turnMetadata map[string]string
	if err := json.Unmarshal([]byte(rawTurnMetadata), &turnMetadata); err != nil {
		t.Fatalf("turn metadata is not JSON: %v; %q", err, rawTurnMetadata)
	}
	if turnMetadata["prompt_cache_key"] != "thread-1" || turnMetadata["window_id"] != "thread-1:0" || turnMetadata["turn_id"] == "" {
		t.Fatalf("turn metadata = %#v, want prompt_cache_key/window_id/turn_id", turnMetadata)
	}
}

func TestApplyCodexMetadataHeadersUsesWindowMetadata(t *testing.T) {
	var req fasthttp.Request
	applyCodexMetadataHeaders(&req, provider.FormatCodexResponses, []byte(`{"prompt_cache_key":"thread-1","client_metadata":{"x-codex-window-id":"thread-1:0","x-codex-turn-metadata":"{\"prompt_cache_key\":\"thread-1\",\"turn_id\":\"turn-1\",\"window_id\":\"thread-1:0\"}"}}`))

	// Upstream codex_cli_rs (core/src/client.rs:654-655, 1729-1731) emits these
	// x-codex-* headers in lowercase only.
	if got := string(req.Header.Peek("x-codex-window-id")); got != "thread-1:0" {
		t.Fatalf("x-codex-window-id = %q, want thread-1:0", got)
	}
	if got := string(req.Header.Peek("x-codex-turn-metadata")); !strings.Contains(got, `"prompt_cache_key":"thread-1"`) {
		t.Fatalf("x-codex-turn-metadata = %q, want prompt_cache_key", got)
	}
	// codex-api/src/requests/headers.rs:5-13 — session-id / thread-id are
	// lowercase hyphenated. x-client-request-id is the ChatGPT backend's
	// canonical request id we mirror for trace correlation. The official
	// client does NOT emit a Conversation_id header, so it must be absent.
	for _, header := range []string{"session-id", "thread-id", "x-client-request-id"} {
		if got := string(req.Header.Peek(header)); got != "thread-1" {
			t.Fatalf("%s = %q, want thread-1", header, got)
		}
	}
	if got := string(req.Header.Peek("Conversation_id")); got != "" {
		t.Fatalf("Conversation_id must not be emitted (official client never sends it), got %q", got)
	}
	// Official codex_cli_rs client does NOT emit a `version` HTTP header.
	// The cargo workspace `version = "0.0.0"` is the package version, not a header.
	// Empty-valued fingerprint headers (X-Codex-Turn-State,
	// X-ResponsesAPI-Include-Timing-Metrics) are also not pre-seeded.
	if testHeaderPresent(&req, "version") {
		t.Fatalf("version header should not be emitted (official client never sends it)")
	}
	for _, header := range []string{"X-Codex-Turn-State", "X-ResponsesAPI-Include-Timing-Metrics"} {
		if testHeaderPresent(&req, header) {
			t.Fatalf("%s should not be pre-seeded (official client only emits when valued)", header)
		}
	}

	var nonCodexReq fasthttp.Request
	applyCodexMetadataHeaders(&nonCodexReq, provider.FormatOpenAIResponses, []byte(`{"client_metadata":{"x-codex-window-id":"thread-1:0"}}`))
	if got := string(nonCodexReq.Header.Peek("x-codex-window-id")); got != "" {
		t.Fatalf("non-Codex request should not receive x-codex-window-id, got %q", got)
	}
}

func TestApplyCodexMetadataHeadersSynthesizesWindowFromPromptCacheKey(t *testing.T) {
	var req fasthttp.Request
	applyCodexMetadataHeaders(&req, provider.FormatCodexResponses, []byte(`{"prompt_cache_key":"thread-1"}`))

	if got := string(req.Header.Peek("x-codex-window-id")); got != "thread-1:0" {
		t.Fatalf("x-codex-window-id = %q, want thread-1:0", got)
	}
}

func TestNormalizeCodexResponsesRequestSanitizesInvalidReasoningEncryptedContent(t *testing.T) {
	validBytes := []byte{0x80}
	validBytes = append(validBytes, make([]byte, 8)...)
	validBytes = append(validBytes, make([]byte, 16)...)
	validBytes = append(validBytes, make([]byte, 16)...)
	validBytes = append(validBytes, make([]byte, 32)...)
	valid := base64.RawURLEncoding.EncodeToString(validBytes)
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"reasoning","encrypted_content":" not-valid "},
			{"type":"reasoning","encrypted_content":123},
			{"type":"reasoning","encrypted_content":"`+valid+`"},
			{"type":"message","role":"user","encrypted_content":"leave-message-alone","content":[{"type":"input_text","text":"hi"}]}
		]
	}`), true, "channel-a:account-a")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("invalid reasoning items should be dropped, got %d input items: %s", len(input), got)
	}
	if input[0].(map[string]interface{})["encrypted_content"] != valid {
		t.Fatalf("valid reasoning encrypted_content should be preserved: %s", got)
	}
	if input[1].(map[string]interface{})["encrypted_content"] != "leave-message-alone" {
		t.Fatalf("non-reasoning encrypted_content should be preserved: %s", got)
	}
}

func TestNormalizeCodexResponsesRequestDropsUnencryptedReasoningItems(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"id":"rs_18b7ed7b7a95d92f","type":"reasoning","summary":[{"type":"summary_text","text":"hidden"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}
		]
	}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("input length = %d, want 2 after dropping reasoning item: %s", len(input), got)
	}
	for idx, rawItem := range input {
		item := rawItem.(map[string]interface{})
		if item["type"] == "reasoning" || item["id"] == "rs_18b7ed7b7a95d92f" {
			t.Fatalf("input[%d] should not contain persisted reasoning history: %s", idx, got)
		}
	}
	if input[0].(map[string]interface{})["role"] != "user" || input[1].(map[string]interface{})["role"] != "assistant" {
		t.Fatalf("message items should remain in order: %s", got)
	}
}

func TestNormalizeCodexResponsesRequestKeepsOnlyEncryptedReasoningPayload(t *testing.T) {
	validBytes := []byte{0x80}
	validBytes = append(validBytes, make([]byte, 8)...)
	validBytes = append(validBytes, make([]byte, 16)...)
	validBytes = append(validBytes, make([]byte, 16)...)
	validBytes = append(validBytes, make([]byte, 32)...)
	valid := base64.RawURLEncoding.EncodeToString(validBytes)
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"hidden"}],"encrypted_content":"`+valid+`"}
		]
	}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("input length = %d, want encrypted reasoning item preserved: %s", len(input), got)
	}
	item := input[0].(map[string]interface{})
	if item["encrypted_content"] != valid {
		t.Fatalf("encrypted_content should be preserved: %s", got)
	}
	for _, key := range []string{"id", "status", "summary"} {
		if _, ok := item[key]; ok {
			t.Fatalf("encrypted reasoning item should not include %s: %s", key, got)
		}
	}
}

func TestNormalizeCodexResponsesRequestStripsMessageReasoningFields(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"assistant","reasoning_content":"hidden","reasoning":"alias","reasoning_details":[{"type":"text","text":"hidden"}],"content":[{"type":"output_text","text":"visible"}]},
			{"type":"reasoning","summary":[],"encrypted_content":"invalid-but-separate"},
			{"role":"user","reasoning_content":"client-leak","content":"next"}
		]
	}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("invalid reasoning item should be dropped: %s", got)
	}
	for idx, rawItem := range input {
		item := rawItem.(map[string]interface{})
		if item["type"] == "reasoning" {
			t.Fatalf("input[%d] should not be a reasoning item after dropping reasoning: %s", idx, got)
		}
		for _, key := range []string{"reasoning_content", "reasoning", "reasoning_details"} {
			if _, ok := item[key]; ok {
				t.Fatalf("message item %d should not include %s: %s", idx, key, got)
			}
		}
	}
}

func testHeaderPresent(req *fasthttp.Request, name string) bool {
	found := false
	req.Header.VisitAll(func(key, _ []byte) {
		if strings.EqualFold(string(key), name) {
			found = true
		}
	})
	return found
}

func TestNormalizeCodexResponsesRequestHonorsConfiguredStreamMode(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi"}`), false, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	if body["stream"] != false {
		t.Fatalf("stream = %#v, want false", body["stream"])
	}
}

func TestNormalizeCodexResponsesRequestIncludesEncryptedReasoningOnlyWithReasoning(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":"hi","reasoning":{"effort":"high"}}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	include, ok := body["include"].([]interface{})
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning.encrypted_content", body["include"])
	}
}

func TestNormalizeCodexResponsesRequestMergesReasoningIncludeAndTextControls(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":"hi",
		"reasoning":{"effort":"high"},
		"include":["message.output_text.logprobs"],
		"verbosity":"low",
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"answer",
				"strict":true,
				"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}
			}
		}
	}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	include, ok := body["include"].([]interface{})
	if !ok || len(include) != 2 || include[0] != "message.output_text.logprobs" || include[1] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want existing include plus reasoning.encrypted_content", body["include"])
	}
	if _, ok := body["response_format"]; ok {
		t.Fatalf("response_format should be converted to text.format: %#v", body)
	}
	if _, ok := body["verbosity"]; ok {
		t.Fatalf("verbosity should be converted to text.verbosity: %#v", body)
	}
	text, ok := body["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("text missing: %#v", body)
	}
	if text["verbosity"] != "low" {
		t.Fatalf("text.verbosity = %#v, want low", text["verbosity"])
	}
	format, ok := text["format"].(map[string]interface{})
	if !ok {
		t.Fatalf("text.format missing: %#v", text)
	}
	if format["type"] != "json_schema" || format["name"] != "answer" || format["strict"] != true {
		t.Fatalf("text.format metadata = %#v", format)
	}
	schema, ok := format["schema"].(map[string]interface{})
	if !ok || schema["type"] != "object" {
		t.Fatalf("text.format.schema = %#v", format["schema"])
	}
}

func TestNormalizeCodexResponsesRequestConvertsSystemInputRole(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"system","content":"rules"}],"service_tier":"priority"}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	first := input[0].(map[string]interface{})
	if first["role"] != "developer" {
		t.Fatalf("role = %#v, want developer", first["role"])
	}
	if body["service_tier"] != "priority" {
		t.Fatalf("priority service_tier should be preserved: %#v", body)
	}
}

func TestNormalizeCodexResponsesRequestFillsMalformedAssistantTextPart(t *testing.T) {
	got := normalizeCodexResponsesRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"user","content":"hi"},
			{"type":"message","role":"assistant","content":[{"type":"output_text"}]},
			{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]
	}`), true, "")
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("normalized body is not JSON: %v", err)
	}
	input := body["input"].([]interface{})
	userContent := input[0].(map[string]interface{})["content"].([]interface{})
	userPart := userContent[0].(map[string]interface{})
	if userPart["type"] != "input_text" || userPart["text"] != "hi" {
		t.Fatalf("user content = %#v, want input_text hi", userPart)
	}
	assistantContent := input[1].(map[string]interface{})["content"].([]interface{})
	assistantPart := assistantContent[0].(map[string]interface{})
	if assistantPart["type"] != "output_text" {
		t.Fatalf("assistant content type = %#v, want output_text", assistantPart["type"])
	}
	if text, ok := assistantPart["text"].(string); !ok || text != "" {
		t.Fatalf("assistant text = %#v, want empty string", assistantPart["text"])
	}
}

func TestCleanJSONUndefinedPlaceholdersRemovesCherryStudioSentinels(t *testing.T) {
	got := cleanJSONUndefinedPlaceholders([]byte(`{"model":"gemini-2.5-pro","temperature":" [undefined] ","include":["reasoning.encrypted_content","[undefined]"],"input":[{"role":"user","content":[{"type":"input_text","text":"hi","cache_control":"[undefined]"}]}],"store":false}`))
	var body map[string]interface{}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("cleaned body is not JSON: %v", err)
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature placeholder should be removed: %s", got)
	}
	input := body["input"].([]interface{})
	first := input[0].(map[string]interface{})
	content := first["content"].([]interface{})
	part := content[0].(map[string]interface{})
	if _, ok := part["cache_control"]; ok {
		t.Fatalf("nested placeholder should be removed: %s", got)
	}
	include := body["include"].([]interface{})
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("array placeholder should be removed: %#v", include)
	}
	if body["store"] != false {
		t.Fatalf("valid false value should be preserved: %#v", body["store"])
	}
}

func TestRelayBodyRewriteHelpersPreserveLargeIntegers(t *testing.T) {
	body := []byte(`{"model":"alias","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"const":9007199254740993}}}}}],"temperature":"[undefined]"}`)

	for name, got := range map[string][]byte{
		"clean":        cleanJSONUndefinedPlaceholders(body),
		"set_model":    setRequestModel(body, "upstream"),
		"inject_model": injectModelIfMissing([]byte(`{"messages":[{"role":"user","content":"hi"}],"value":9007199254740993}`), "upstream"),
		"stream":       injectStreamTrue(body),
	} {
		if !json.Valid(got) {
			t.Fatalf("%s result is not JSON: %s", name, got)
		}
		if !strings.Contains(string(got), `9007199254740993`) {
			t.Fatalf("%s lost integer precision: %s", name, got)
		}
	}
}

func TestConvertedStreamFieldInjectionTargetsUpstreamProtocol(t *testing.T) {
	if shouldInjectStreamField(provider.FormatGemini, false, false, false) {
		t.Fatalf("Gemini client request body must not receive raw stream field before conversion")
	}
	if !shouldInjectConvertedStreamField(provider.FormatOpenAIChatCompletions) {
		t.Fatalf("OpenAI Chat upstream body must receive stream field after conversion")
	}
	if !shouldInjectConvertedStreamField(provider.FormatAnthropic) {
		t.Fatalf("Anthropic upstream body must receive stream field after conversion")
	}
	if shouldInjectConvertedStreamField(provider.FormatGemini) {
		t.Fatalf("Gemini upstream body must not receive OpenAI-style stream field after conversion")
	}
}
