# UAPI Protocol Conversion Layer Redesign

**Date:** 2026-05-28
**Status:** Draft
**Scope:** Rewrite the relay provider conversion layer with strong-typed schema structs and bidirectional conversion methods, referencing bifrost's architecture.

## Background

UAPI's current protocol conversion layer uses `map[string]interface{}` extensively, which leads to:
- Runtime type assertion failures and subtle bugs
- Lost fields during cross-protocol conversion (ExtraParams is a band-aid)
- The critical `instructions` bug: Chat → Responses omits `instructions` when no system message exists
- No compile-time safety for field names or types
- Streaming state machines allocated per-request without pooling

## Design Goals

1. **Strong-typed schema structs** for all 5 protocols with custom JSON marshal/unmarshal
2. **Bidirectional conversion methods** on schema structs (like bifrost's `ToChatMessages`/`ToResponsesMessages`)
3. **`Instructions *string` as first-class field** on InternalRequest — always serialized in Responses output
4. **sync.Pool streaming state machines** for high-throughput scenarios
5. **Same-format passthrough** bypasses Internal round-trip entirely
6. **Preserve unknown fields** via `json.RawMessage` instead of `interface{}`

## Protocol Scope

5 distinct protocols:

| Format Constant | Protocol | Key Differences |
|---|---|---|
| `FormatOpenAIChat` | OpenAI Chat Completions API | `/v1/chat/completions`, messages array |
| `FormatOpenAIResponses` | OpenAI Responses API | `/v1/responses`, instructions + input array |
| `FormatAnthropic` | Anthropic Messages API | `/v1/messages`, top-level `system` field |
| `FormatGemini` | Gemini REST API | `/v1beta/` paths, `contents` + `systemInstruction` |
| `FormatGeminiCLI` | Gemini CLI / Antigravity | `v1internal` paths, `{model,project,request}` envelope |

## Section 1: Schema Structs

### Directory Layout

```
internal/relay/provider/
├── schema/
│   ├── openai_chat.go       # OpenAIChatRequest / OpenAIChatResponse
│   ├── openai_responses.go  # OpenAIResponsesRequest / OpenAIResponsesResponse
│   ├── anthropic.go         # AnthropicRequest / AnthropicResponse
│   ├── gemini.go            # GeminiRequest / GeminiResponse
│   ├── gemini_cli.go        # GeminiCLIRequest / GeminiCLIResponse
│   ├── common.go            # Shared types: ContentPart, ToolCall, ToolChoice, Usage
│   └── json.go              # Custom JSON marshal/unmarshal utilities
├── convert.go               # Schema registry + dispatch
├── streaming.go             # StreamConverter interface + registry
└── [provider dirs]          # Adaptor implementations (streamlined)
```

### Content Polymorphism

Following bifrost's pointer-discriminated union pattern:

```go
type MessageContent struct {
    Text  *string        // bare string content
    Parts []ContentPart  // array of typed content blocks
}

func (mc MessageContent) MarshalJSON() ([]byte, error) {
    if mc.Text != nil && mc.Parts != nil {
        return nil, fmt.Errorf("both Text and Parts are set")
    }
    if mc.Text != nil {
        return json.Marshal(*mc.Text)
    }
    if mc.Parts != nil {
        return json.Marshal(mc.Parts)
    }
    return json.Marshal(nil)
}

func (mc *MessageContent) UnmarshalJSON(data []byte) error {
    // Try string first, then array
    var s string
    if json.Unmarshal(data, &s) == nil {
        mc.Text = &s
        return nil
    }
    var parts []ContentPart
    if err := json.Unmarshal(data, &parts); err != nil {
        return err
    }
    mc.Parts = parts
    return nil
}
```

### Instructions vs System Message

Schema structs preserve protocol-native representation:
- `OpenAIChatRequest`: system messages are in `Messages` array with `role: "system"`
- `OpenAIResponsesRequest`: `Instructions json.RawMessage` is a top-level field
- `AnthropicRequest`: `System json.RawMessage` is a top-level field
- `GeminiRequest`: `SystemInstruction *GeminiContent` is a top-level field

Conversion to InternalRequest extracts all of these into a unified `Instructions *string`.

### Gemini CLI Envelope

```go
type GeminiCLIRequest struct {
    Model     string        `json:"model"`
    Project   string        `json:"project"`
    UserAgent string        `json:"userAgent,omitempty"`
    Request   GeminiRequest `json:"request"`
    // Antigravity-specific fields
    RequestType  string `json:"requestType,omitempty"`
    RequestID    string `json:"requestId,omitempty"`
    SessionID    string `json:"sessionId,omitempty"`
}
```

### Unknown Field Preservation

Use `json.RawMessage` for protocol-specific or uncertain fields:

```go
type OpenAIChatRequest struct {
    Model       string          `json:"model"`
    Messages    []ChatMessage   `json:"messages"`
    Tools       json.RawMessage `json:"tools,omitempty"`
    ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
    Reasoning   json.RawMessage `json:"reasoning,omitempty"`
    // ... well-known typed fields
    Extra       map[string]json.RawMessage `json:"-"` // populated from unknown JSON keys
}
```

Custom `UnmarshalJSON` captures unrecognized top-level keys into `Extra`.

## Section 2: InternalRequest + Bidirectional Conversion

### InternalRequest (Simplified)

```go
type InternalRequest struct {
    Model    string
    Stream   bool
    Messages []InternalMessage
    Tools    []InternalTool

    // Generation params (pointer = unset vs zero value)
    MaxTokens   *int
    Temperature *float64
    TopP        *float64
    TopK        *int
    StopWords   []string

    // Unified system prompt
    Instructions *string

    // Protocol-specific fields passed through as raw JSON
    Reasoning      json.RawMessage
    ToolChoice     json.RawMessage
    ResponseFormat json.RawMessage
    ParallelToolCalls *bool

    // Preserve protocol-specific fields for same-format passthrough
    Extra map[string]json.RawMessage

    // Source format for selective field restoration
    SourceFormat Format
}
```

### InternalMessage

```go
type InternalMessage struct {
    Role             string // "user", "assistant", "tool" (NOT "system" or "developer")
    Content          []InternalContentPart
    ToolCalls        []InternalToolCall
    ToolResult       *InternalToolResult
    ReasoningContent []InternalContentPart
    Name             string // for named messages
}
```

System/developer messages are **always** extracted to `Instructions` during `ToInternal()`. The `Messages` slice only contains `user`, `assistant`, and `tool` roles.

### Bidirectional Method Signatures

```go
// OpenAI Chat
func (r *OpenAIChatRequest) ToInternal() (*InternalRequest, error)
func InternalToOpenAIChat(ir *InternalRequest) (*OpenAIChatRequest, error)

// OpenAI Responses
func (r *OpenAIResponsesRequest) ToInternal() (*InternalRequest, error)
func InternalToOpenAIResponses(ir *InternalRequest) (*OpenAIResponsesRequest, error)

// Anthropic
func (r *AnthropicRequest) ToInternal() (*InternalRequest, error)
func InternalToAnthropic(ir *InternalRequest) (*AnthropicRequest, error)

// Gemini
func (r *GeminiRequest) ToInternal() (*InternalRequest, error)
func InternalToGemini(ir *InternalRequest) (*GeminiRequest, error)

// Gemini CLI (delegates to Gemini + envelope)
func (r *GeminiCLIRequest) ToInternal() (*InternalRequest, error)
func InternalToGeminiCLI(ir *InternalRequest) (*GeminiCLIRequest, error)
```

### Instructions Conversion Semantics

| Direction | Source | Target | Mapping Rule |
|---|---|---|---|
| Chat → Internal | `messages[role=system/developer]` | `Instructions` | Merge all system/developer text |
| Responses → Internal | `instructions` field | `Instructions` | Direct assignment |
| Anthropic → Internal | `system` field | `Instructions` | Extract text from blocks |
| Gemini → Internal | `systemInstruction` | `Instructions` | Extract text from parts |
| Internal → Chat | `Instructions` | `messages[role=system]` | Prepend as first system message |
| Internal → Responses | `Instructions` | `instructions` field | **Always write (including empty string)** |
| Internal → Anthropic | `Instructions` | `system` field | Write as system content blocks |
| Internal → Gemini | `Instructions` | `systemInstruction` | Write as systemInstruction content |

**Critical fix:** `Internal → Responses` always emits `instructions` field, even when empty string. This prevents the "Instructions are required" error from upstream.

### Schema Registry

```go
var schemaRegistry = map[Format]struct {
    toInternal   func(body []byte) (*InternalRequest, error)
    fromInternal func(ir *InternalRequest) ([]byte, error)
}{
    FormatOpenAIChat:      {(*OpenAIChatRequest).toInternalFromBytes, internalToOpenAIChatBytes},
    FormatOpenAIResponses: {(*OpenAIResponsesRequest).toInternalFromBytes, internalToOpenAIResponsesBytes},
    FormatAnthropic:       {(*AnthropicRequest).toInternalFromBytes, internalToAnthropicBytes},
    FormatGemini:          {(*GeminiRequest).toInternalFromBytes, internalToGeminiBytes},
    FormatGeminiCLI:       {(*GeminiCLIRequest).toInternalFromBytes, internalToGeminiCLIBytes},
}
```

## Section 3: Streaming Conversion State Machines

### StreamConverter Interface

```go
type StreamConverter interface {
    Convert(line []byte) []byte // Input: one SSE line; Output: zero or more SSE lines
    Done() []byte               // Final event(s) when stream ends
    Reset()                     // Clear state before returning to pool
}
```

### Registry

```go
type FormatPair struct {
    Upstream, Client Format
}

var streamConverterRegistry = map[FormatPair]func() StreamConverter{
    {FormatOpenAIResponses, FormatOpenAIChat}:  NewResponsesToChatStreamConverter,
    {FormatOpenAIChat, FormatOpenAIResponses}:  NewChatToResponsesStreamConverter,
    {FormatAnthropic, FormatOpenAIChat}:        NewAnthropicToChatStreamConverter,
    {FormatOpenAIChat, FormatAnthropic}:        NewChatToAnthropicStreamConverter,
    {FormatGemini, FormatOpenAIChat}:           NewGeminiToChatStreamConverter,
    {FormatGeminiCLI, FormatOpenAIChat}:        NewGeminiToChatStreamConverter, // reuse Gemini
    {FormatOpenAIChat, FormatGemini}:           NewChatToGeminiStreamConverter,
    {FormatOpenAIChat, FormatGeminiCLI}:        NewChatToGeminiStreamConverter, // reuse Gemini
}
```

### sync.Pool Pattern (from bifrost)

```go
var responsesToChatPool = sync.Pool{
    New: func() interface{} {
        return &responsesToChatState{
            toolCallArgs:      make(map[string]*strings.Builder),
            toolCallNames:     make(map[string]string),
            toolCallIDToIndex: make(map[string]int),
            textBuffer:        strings.Builder{},
        }
    },
}

func acquireResponsesToChatState() *responsesToChatState {
    s := responsesToChatPool.Get().(*responsesToChatState)
    return s
}

func releaseResponsesToChatState(s *responsesToChatState) {
    // Reset all fields
    for k := range s.toolCallArgs {
        delete(s.toolCallArgs, k)
    }
    // ... clear all maps and buffers
    s.textBuffer.Reset()
    responsesToChatPool.Put(s)
}
```

### Event Mapping: Responses → Chat SSE

| Responses SSE Event | Chat SSE Output |
|---|---|
| `response.created` | Skip (record model/ID/timestamp) |
| `response.in_progress` | Skip |
| `response.output_item.added` (message) | First chunk with `role: "assistant"` |
| `response.content_part.added` | Skip |
| `response.output_text.delta` | `delta.content` chunk |
| `response.output_item.added` (function_call) | `delta.tool_calls[i]` with ID + name |
| `response.function_call_arguments.delta` | `delta.tool_calls[i].function.arguments` delta |
| `response.output_item.done` (function_call) | Tool call arguments finalization |
| `response.output_text.done` | Skip |
| `response.content_part.done` | Skip |
| `response.output_item.done` (message) | Skip |
| `response.completed` | `finish_reason` + usage + `[DONE]` |
| `response.incomplete` | `finish_reason: "length"` + `[DONE]` |
| `response.error` / `response.failed` | Error chunk |

### Token Fallback Chain (from new-api)

Three-tier fallback for usage:
1. Explicit usage from `response.completed` event
2. Explicit usage from non-streaming response (if available)
3. Local estimation based on accumulated text length

### Tool Call Canonical ID Mapping (from new-api)

Responses API `item.id` and `call_id` may differ between `output_item.added` and `function_call_arguments.delta` events. Maintain a bidirectional mapping:

```go
type responsesToChatState struct {
    // ...
    itemIDToCallID   map[string]string // item.id → call_id
    callIDToIndex    map[string]int    // call_id → tool_calls array index
    callIDToName     map[string]string // call_id → function name
    callIDArgs       map[string]*strings.Builder
}
```

## Section 4: Adaptor Interface + Conversion Dispatch

### Simplified Adaptor

```go
type Adaptor interface {
    Init(channel *db.Channel, account *db.Account)
    SetRequestParams(model string, stream bool)
    GetRequestURL(path string) (string, error)
    SetupRequestHeader(req *fasthttp.Request, credentials string) error
    Format() Format
    CreateStreamConverter(upstream, client Format) StreamConverter
    ParseUsage(respBody []byte) (promptTokens, completionTokens int, err error)
    ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
}
```

Conversion is no longer the Adaptor's responsibility — it's handled by the schema registry.

### Conversion Dispatch (in handler.go)

```
Client request → parse clientFormat from URL path
              → parse upstreamFormat from channel.Type + channel.APIFormat
              → sameFormat?
                  → YES: JSON passthrough (inject model, stream only)
                  → NO:  schemaRegistry[clientFormat].toInternal(body)
                         → InternalRequest
                         → schemaRegistry[upstreamFormat].fromInternal(ir)
              → send to upstream
```

### Same-Format Passthrough

When `clientFormat == upstreamFormat`, do NOT go through Internal round-trip. Instead:
- Parse original JSON into a `map[string]interface{}` (or use gjson/sjson for surgical edits)
- Replace `model` field
- Inject `stream: true` if force stream is active
- Inject `stream_options.include_usage: true` if needed
- Forward all other fields as-is

This eliminates the "round-trip loses unknown fields" problem entirely.

### Gemini CLI (Antigravity) Special Handling

```go
func internalToGeminiCLI(ir *InternalRequest) ([]byte, error) {
    // 1. Delegate to Gemini conversion
    geminiBody, err := internalToGemini(ir)
    if err != nil {
        return nil, err
    }

    // 2. Wrap in Antigravity envelope (reference CLIProxyAPI)
    var req GeminiCLIRequest
    req.Model = ir.Model
    req.Project = ""
    req.UserAgent = "antigravity"
    req.RequestType = antigravityRequestType(ir.Model) // "image_gen" or "agent"
    req.RequestID = uuid.New().String()

    // 3. Parse Gemini body into inner request
    json.Unmarshal(geminiBody, &req.Request)

    // 4. Compute session ID from first user message
    req.SessionID = computeSessionID(ir.Messages)

    // 5. Claude-specific: validated function calling
    if isClaudeModel(ir.Model) {
        setValidatedToolConfig(&req.Request)
    }

    // 6. Non-Claude: remove maxOutputTokens, remove safetySettings
    if !isClaudeModel(ir.Model) {
        removeMaxOutputTokens(&req.Request)
        req.Request.SafetySettings = nil
    }

    return json.Marshal(req)
}
```

## Section 5: Response Conversion + Error Handling

### InternalResponse

```go
type InternalResponse struct {
    ID      string
    Model   string
    Choices []InternalChoice
    Usage   InternalUsage
    Raw     json.RawMessage // preserved for same-format passthrough
}

type InternalChoice struct {
    Index            int
    Role             string
    Content          []InternalContentPart
    ToolCalls        []InternalToolCall
    FinishReason     string
    ReasoningContent []InternalContentPart
}
```

### Same-Format Response Passthrough

When `clientFormat == upstreamFormat`, return upstream response bytes directly. No deserialization/serialization.

### Cross-Format Response Conversion

```
Upstream response → responseRegistry[upstreamFormat].ToInternal(body)
                 → InternalResponse
                 → responseRegistry[clientFormat].FromInternal(ir)
                 → Client response bytes
```

### Error Handling

1. **Conversion errors** → 400 with `relay_error`:
   ```json
   {"error": {"message": "conversion failed: <reason>", "type": "relay_error"}}
   ```

2. **Unsupported features** — two strategies:
   - **Silent drop**: e.g., Anthropic `cache_control` when converting to non-Anthropic
   - **Error reject**: e.g., Anthropic `thinking_delta` when no conversion path exists

3. **Error format normalization** — keep existing `normalizeErrorResponse`:
   - OpenAI: `{"error":{"message":"...","type":"relay_error"}}`
   - Anthropic: `{"type":"error","error":{"type":"api_error","message":"..."}}`
   - Gemini: `{"error":{"code":400,"message":"...","status":"INVALID_ARGUMENT"}}`

## Section 6: Bug Fix Checklist

Bugs fixed as part of this redesign:

| Bug | Current Behavior | Fix |
|---|---|---|
| **instructions missing** | Chat → Responses omits `instructions` when no system message | Always write `instructions` field (including empty string) |
| **ExtraParams cross-protocol loss** | Unknown fields silently dropped on cross-protocol | `Extra map[string]json.RawMessage` + selective extraction in converters |
| **Same-format round-trip loss** | ToInternal → FromInternal loses unknown fields | Same-format JSON passthrough, no Internal round-trip |
| **Anthropic thinking one-way** | `thinking_delta` streaming events skipped | Implement thinking → ReasoningContent conversion |
| **Gemini thoughtSignature dropped** | Silently ignored in streaming | Preserve in ReasoningContent Extra field |
| **Tool call ID generation inconsistency** | Gemini response tool call IDs are FIFO-generated | Use deterministic ID generation referencing CLIProxyAPI |
| **Responses stream tool call ID mismatch** | `item.id` vs `call_id` may differ | Canonical ID mapping (from new-api pattern) |

## References

- **bifrost** (`upstream/bifrost/`): Schema structs, bidirectional methods, sync.Pool streaming, compat plugin
- **new-api** (`upstream/new-api/`): Instructions promotion, policy-based routing, token fallback chain, tool call canonical ID
- **CLIProxyAPI** (`upstream/CLIProxyAPI/`): Antigravity envelope wrapping, Gemini CLI protocol, thought signature handling
