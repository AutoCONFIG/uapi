# UAPI 协议转换层验收检查提示词

请你对当前 UAPI 项目刚完成的协议转换层重构进行一次全面、严格、源码驱动的验收审查，并在发现问题后直接修复。

项目路径：

`/media/yun/de2a43ce-446c-4a62-99b3-8ddc6ea1ef87/UAPI`

当前产品尚未正式上线生产，不需要保留历史兼容包袱。不要因为旧逻辑存在就继续兼容；如果旧 alias、旧中间结构、重复 converter、临时 shim 不符合长期架构，请删除或迁移。

## 先审查当前改动

在阅读源码前，先执行并审查：

- `git status --short`
- `git diff --stat`
- `git diff`

目的：

- 识别本轮重构实际改动范围。
- 找出遗漏文件、无关修改、生成代码噪音。
- 不回滚用户已有改动，但在报告中指出可疑或无关改动。

## 核心验收目标

检查协议转换层是否真正达到长期架构目标：

- 是否建立协议中立、高保真、可扩展的 IR。
- 是否消除旧转换层的双事实来源、临时兼容 alias、重复 converter、死代码。
- 是否正确覆盖 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini GenerateContent、Gemini CLI / Code Assist、Claude Code、Codex Responses、Antigravity。
- 是否保留 raw/native metadata。
- 是否显式记录不可逆转换 loss。
- 是否补齐 stream、tool call、多模态、reasoning/thinking、usage、错误处理测试。
- 是否删除重构后过时、无用、冗余代码。

请先输出一个短但高密度的审查汇总，然后继续修复，不要停在建议。

## 必须遵守的格式判断规则

- 下游/client 协议格式由请求路径和 request type 判断，不由模型名判断。
- 上游协议格式由 channel `Type` + `APIFormat` 判断，不由模型名判断。
- 模型名只参与 channel/account/model 路由和模型映射。
- 同协议必须尽可能完整参考 Bifrost 经过生产验证的模式：
  parse/classify/validate 之后保留并转发 native body。
- 同协议不走跨协议语义转换。
- 同协议也不能盲目裸透传：必须做 sanitizer + 同协议 parser validation。这是
  Bifrost-style raw-body preservation，不是跳过解析和校验。
- 跨协议必须走 IR/adaptor 转换，并记录不可逆 loss。

UAPI 期望请求流：

1. 从 HTTP path/request type 识别 downstream/client format：
   `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、Gemini `generateContent` / `streamGenerateContent`、media endpoint、WS bridge event。
2. 从 channel `Type` + `APIFormat` 识别 upstream format：
   `openai` + `standard|responses|codex`，`anthropic` + `standard|claude_code`，`gemini` + `standard|gemini_code`，或 `antigravity`。
3. 如果 client format == upstream format，调用 `NormalizeRequestSameProtocol`：
   清理 JSON 中字面量 `"[undefined]"`，用该协议 parser 校验，然后转发清理后的原 body，不通过 IR emitter 重建。
4. 如果格式不同，parse 到协议中立 IR，prepare target，再通过 adaptor/converter emit。
5. provider 特定 request shaping 只应在 provider/adaptor 层发生。

## Bifrost 对照结论必须复核

同协议规则必须优先对齐 Bifrost 的 raw request preservation 模式，而不只是泛泛参考。Bifrost 是经过生产验证的参考实现：它会解析 integration request 以识别 request type/model/stream/validation 等信息；对 native provider route，尤其 Gemini GenAI，会设置 `BifrostContextKeyUseRawRequestBody` 并携带 `RawRequestBody` 到 provider，避免 order-sensitive/provider-native JSON 被无意义重序列化。

UAPI 的同协议实现应尽量贴近这个模式。`NormalizeRequestSameProtocol` 允许多做一层已知客户端噪声清理，例如 Cherry Studio 的字面量 `"[undefined]"`，但清理后仍应保持 native body preservation，不应重建协议 body。如果审查发现 UAPI 和 Bifrost 的同协议 raw-body 保留语义有差异，默认应让 UAPI 向 Bifrost 靠拢，除非有明确记录的 UAPI 特有原因。

请重点核对：

- `upstream/bifrost/core/providers/utils/utils.go`
  - `CheckContextAndGetRequestBody`
  - `CheckAndGetRawRequestBody`
- `upstream/bifrost/core/schemas/chatcompletions.go`
  - `BifrostChatRequest.RawRequestBody`
- `upstream/bifrost/core/schemas/responses.go`
  - `BifrostResponsesRequest.RawRequestBody`
- `upstream/bifrost/transports/bifrost-http/integrations/router.go`
  - request parsing/conversion flow
  - `BifrostContextKeyUseRawRequestBody`
- `upstream/bifrost/transports/bifrost-http/integrations/genai.go`
  - `setGenAIRawRequestBodyFromRequest`
  - Gemini native raw-body handling
- `upstream/bifrost/transports/bifrost-http/integrations/genai_test.go`
  - `TestExtractAndSetModelAndRequestTypePreservesRawBodyForGenerateContent`

UAPI 中的对应验收点：

- `internal/relay/handler.go` 同协议 HTTP 请求必须走 `NormalizeRequestSameProtocol`，不是 `ConvertRequestWithAdaptor`。
- `internal/relay/ws_bridge.go` 的 WS `response.create` HTTP bridge 同协议也必须走 `NormalizeRequestSameProtocol`。
- `internal/relay/provider/convert/registry.go` 的 `NormalizeRequestSameProtocol` 必须只做 sanitizer + parser validation，不做 emitter rebuild。
- Gemini same-format stream 必须保留 native URL/body 语义，不要注入 OpenAI 风格 JSON `stream` 字段。

## 必须阅读的 UAPI 范围

不要抽样。先用 `rg --files`、`rg` 系统梳理并阅读相关代码。至少覆盖：

- `internal/relay/request_type.go`
- `internal/relay/handler.go`
- `internal/relay/ws_bridge.go`
- `internal/relay/stream_converter.go`
- `internal/relay/provider`
- `internal/relay/provider/convert`
- `internal/relay/provider/schema`
- `internal/relay/provider/ir`
- `internal/relay/provider/stream`
- 所有 provider/channel adaptor
- 下游 OpenAI / Anthropic / Gemini / Claude Code / Codex compatible API 层
- stream parser / stream writer
- usage / billing / quota
- error normalization
- model mapping / channel mapping
- 所有 conversion / protocol / stream / tool / multimodal / usage / error 测试

## 必须对照的 upstream 范围

至少覆盖：

- `upstream/bifrost/core/schemas`
- `upstream/bifrost/core/providers`
- `upstream/bifrost/core/internal/llmtests`
- `upstream/codex/codex-rs/codex-client/src`
- `upstream/codex/codex-rs/codex-api/src`
- `upstream/codex/codex-rs/protocol/src/models.rs`
- `upstream/codex/codex-rs/core/src/stream_events_utils.rs`
- `upstream/gemini-cli/packages/core/src/code_assist`
- `upstream/gemini-cli/packages/core/src/core`
- `upstream/gemini-cli/packages/core/src/utils`
- `upstream/claude-code-source-1/src`
- `upstream/claude-code-source-2/src`
- `upstream/claude-code-source-3/src`
- `upstream/claude-code-source-3/packages`
- `upstream/CLIProxyAPI/internal/translator`
- `upstream/CLIProxyAPI/internal/runtime/executor`
- `upstream/CLIProxyAPI/internal/auth`
- `upstream/CLIProxyAPI/internal/thinking`
- `upstream/new-api/dto`
- `upstream/new-api/relay/channel/openai`
- `upstream/new-api/relay/channel/claude`
- `upstream/new-api/relay/channel/gemini`
- `upstream/new-api/relay/channel/codex`
- `upstream/new-api/service/openaicompat`
- `upstream/Antigravity-Manager/src`
- `upstream/Antigravity-Manager/docs`
- `upstream/cockpit-tools/src/utils/antigravity*`
- `upstream/cockpit-tools/src/services/antigravity*`

## 第一阶段：审查汇总

先输出：

1. 当前重构完成度。
2. 是否仍存在旧转换层残留。
3. 是否仍存在双事实来源。
4. 哪些协议 adapter 覆盖不足。
5. 哪些信息可能仍被静默丢失。
6. 哪些测试缺失或不足。
7. 哪些代码应删除但未删除。
8. 哪些问题必须立即修复。

这一步不是最终报告，汇总后继续修复。

## 重点检查项

### IR 设计

确认 IR 不是某个协议的变体，能表达：

- system / developer / user / assistant / tool / function / model / unknown role
- ordered item / content part
- text / image / audio / video / file / document
- tool_use / tool_result
- function_call / function_call_output
- reasoning / thinking / redacted_thinking / encrypted_reasoning
- refusal / citation
- executable_code / code_execution_result
- cache marker / cache control
- safety block / moderation block
- opaque/native item
- raw request / raw response / raw stream event
- provider metadata / unknown fields / loss records

重点搜索旧事实来源：

- `InternalRequest`
- `InternalMessage`
- `InternalResponse`
- `Content`
- `ToolCalls`
- `ToolResult`
- `ReasoningContent`
- `ContentPart`
- `Parts`
- provider/types alias
- deprecated converter
- legacy shim

如果这些仍存在，区分合理保留、测试 fixture、死代码、生产路径旧架构残留。死代码和旧架构残留请直接删除或迁移。

### Loss Accounting

检查所有不可逆转换是否显式记录 loss，至少覆盖：

- Claude `cache_control`
- Anthropic thinking / redacted thinking
- Codex `reasoning.encrypted_content`
- Gemini `thoughtSignature`
- Gemini safety/block reason
- OpenAI Responses item lifecycle 被 Chat delta 压平
- Codex `function_call_output.output` 结构化内容被 string 化
- Gemini `functionResponse.parts/id/willContinue/scheduling`
- Gemini snake_case / camelCase 差异
- provider-specific generation config
- unknown fields

发现静默丢弃时，修复为 raw/native metadata、opaque item、loss record，最好三者结合。

### Cherry Studio 回归

必须保持修复：

- `/v1/responses`
- `/v1beta/models/{model}:streamGenerateContent?alt=sse`
- `/v1/messages`

这些入口可能收到字面量 `"[undefined]"`：

- Responses: `temperature`、`parallel_tool_calls`、`instructions`、`tools`、嵌套 content 字段，PDF/file 可用 `input_file.file_data`。
- Gemini: `generationConfig.maxOutputTokens`、`generationConfig.temperature`、`systemInstruction`、`tools`。
- Anthropic: `temperature`、`system`、`cache_control`、`tools`。

### Streaming

检查 stream IR 和各协议 SSE 输出是否完整：

- OpenAI Chat SSE
- OpenAI Responses SSE
- Anthropic Messages SSE
- Gemini SSE
- Gemini CLI / Code Assist SSE
- Claude Code stream
- Codex Responses SSE
- Antigravity stream

Codex Responses SSE 必须支持：

- `response.created`
- `response.output_item.added`
- `response.content_part.added`
- `response.output_text.delta`
- `response.content_part.done`
- `response.output_item.done`
- `response.completed`
- error event

Gemini SSE 必须正确处理多行 `data:`。

### Tool Call / Function Call

检查：

- tool call id 稳定保留。
- tool result 正确关联 tool call。
- OpenAI function call 与 tool call 差异被保留。
- Anthropic `tool_use` / `tool_result`。
- Gemini `functionCall` / `functionResponse`。
- Codex `function_call` / `function_call_output`。
- Claude Code OAuth tool name rewrite。
- Antigravity function call/response grouping。
- arguments raw JSON 保留。
- tool schema key order 尽量保留。

### 多模态

检查转换是否覆盖：

- image URL / image data URI / inline image bytes
- file id / inline file bytes / document/PDF
- audio / video
- mime type / filename / provider file metadata

不能用单一 text 字段吞掉多模态内容。

### Usage / Billing / Quota

检查 usage 是否保留：

- input/output/total tokens
- prompt/completion tokens
- cache read/write tokens
- reasoning/audio/image/text/tool tokens
- provider raw usage
- estimated usage 标记
- billing usage

检查 billing/quota 是否仍依赖旧字段或丢失 provider usage。

### Error Normalization

至少覆盖：

- auth error
- invalid/expired OAuth token
- refresh token reused
- quota exceeded / rate limit
- context length
- content filter / safety block
- malformed stream
- upstream 5xx
- provider-specific error metadata
- request id / trace id / headers

### 官方客户端类渠道

Codex：

- 按 Responses API 建模。
- request body 字段正确。
- SSE event lifecycle 完整。
- `function_call_output`。
- `reasoning.encrypted_content`。
- usage-limit / context-length / policy error。
- request id / upstream header 保留。

Gemini CLI / Code Assist：

- endpoint: `v1internal:generateContent`、`v1internal:streamGenerateContent?alt=sse`
- envelope: `model`、`project`、`user_prompt_id`、`request`、`enabled_credit_types`
- inner request: `contents`、`systemInstruction`、`cachedContent`、`tools`、`toolConfig`、`labels`、`safetySettings`、`generationConfig`、`session_id`
- `generationConfig` 字段完整保留。
- OAuth / quota / Google error 正确。

Claude Code：

- `anthropic-version`
- `anthropic-beta`
- OAuth token/session/account metadata
- beta headers
- tool name rewrite
- thinking/redacted thinking
- prompt caching
- MCP/builtin tools
- stream events

Antigravity：

- `cloudcode-pa.googleapis.com`
- daily/sandbox/prod fallback
- `/v1internal:generateContent`
- `/v1internal:streamGenerateContent`
- `/v1internal:countTokens`
- HTTP/1.1 / Node-like behavior
- OAuth client id/secret
- project id 注入
- quota / credit retry
- `thoughtSignature`
- `skip_thought_signature_validator`
- `thinkingBudget` vs `thinkingLevel`
- `googleSearch` 与 `functionDeclarations` 冲突规避
- model normalization
- image/text quota 区分

## 第二阶段：修复

发现问题后直接修复：

- 优先修复架构性问题。
- 删除旧代码和冗余代码。
- 补齐缺失 tests。
- 不引入新的双事实来源。
- 不通过临时 adapter 掩盖 IR 表达能力不足。
- 不静默丢字段。
- 不只改测试绕过问题。
- 不破坏用户已有未提交修改。
- 不使用 destructive git 命令。

删除代码前用 `rg` 确认引用。

## 第三阶段：验证

至少运行：

- conversion / protocol tests
- provider adapter tests
- stream tests
- tool call tests
- multimodal tests
- usage tests
- error tests
- `go test -count=1 ./...`

如部分测试无法运行，说明原因，并运行更小范围替代测试。

## 最终报告

最终输出：

1. 审查发现的问题清单。
2. 已修复的问题。
3. 删除的旧代码/冗余代码。
4. 修改的关键文件。
5. 新增/修改的测试。
6. 执行的测试命令和结果。
7. 仍需后续处理的风险。
8. 与 upstream 官方/竞品实现对照后的关键结论。

报告必须具体到源码路径，不写泛泛结论。

## 证据矩阵

最终报告给出简短证据矩阵。每个核心能力至少对应：

- UAPI 实现文件。
- upstream 对照依据。
- 测试文件。
- 是否存在 loss record。
- 是否仍有风险。

核心能力包括：

- ordered IR item
- raw/native preservation
- loss accounting
- OpenAI Chat
- OpenAI Responses
- Anthropic Messages
- Gemini GenerateContent
- Gemini CLI / Code Assist
- Codex Responses
- Claude Code OAuth/native
- Antigravity
- streaming lifecycle
- tool call / tool result
- multimodal
- reasoning/thinking
- usage
- error normalization

## 架构门槛

如果发现新 IR 仍然只是某个协议的变体，或仍然依赖旧字段作为事实来源，不要只修局部 bug。必须继续架构级修复，直到：

- ordered item 是唯一事实来源。
- raw/native/loss 机制覆盖所有 adapter。
- provider adaptor 不再直接依赖旧 convert/schema 中间结构。
- 测试覆盖主要协议双向转换和不可逆转换。
