请你对当前 UAPI 项目进行一次以工程落地为目标的协议转换层彻底重构和严格复查。

项目路径：

`/media/yun/de2a43ce-446c-4a62-99b3-8ddc6ea1ef87/UAPI`

当前项目仍处于早期开发阶段，尚未正式上线，因此不需要背负历史兼容性包袱。请优先追求正确、清晰、可维护、可扩展的长期架构。如果现有自定义中间转换层设计不适合作为长期协议桥接核心，请直接重构，不要只做表面修补。

## 最高原则

本次任务必须以源码和真实实现为依据。

不要只凭经验、文档印象或少量抽样文件下结论。你必须系统阅读当前项目源码，并完整检查 `upstream` 目录中与协议对接、转换、OAuth、流式、tool call、多模态、usage、错误处理相关的代码。

尤其注意：

- 不要只参考 Bifrost，但同协议 raw-body preservation 逻辑必须尽可能完整参考 Bifrost，因为它是经过生产验证的实现。
- 不要抽样式阅读上游实现。
- 官方客户端真实源码行为优先于通用协议文档。
- 成熟 API 网关架构优先于临时拼接式实现。
- 竞品实现可用于补足缺少官方源码的渠道，尤其 Antigravity。
- 协议转换必须高保真、低损耗、可扩展。
- 无法表达或不可逆转换的信息必须显式记录 loss，并尽量保留原始 metadata/raw。
- 重构完成后，必须删除过时、无用、重复、已经被新架构取代的旧代码。

## 当前必须遵守的格式判断规则

请特别复查并严格遵守以下规则：

- 下游/client 协议格式由请求路径和 request type 判断，不由模型名判断。
- 上游协议格式由 channel `Type` + `APIFormat` 判断，不由模型名判断。
- 模型名只参与 channel/account/model 路由和模型映射。
- 同协议不走跨协议语义转换。
- 同协议也不能盲目裸透传；必须做 sanitizer + 同协议 parser validation。
- 同协议逻辑必须尽可能完整参考 Bifrost 经过生产验证的模式：parse/classify/validate 之后保留并转发 native body。
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

## Bifrost 同协议逻辑必须重点对齐

同协议规则必须优先对齐 Bifrost 的 raw request preservation 模式，而不只是泛泛参考。Bifrost 是经过生产验证的参考实现：它会解析 integration request 以识别 request type/model/stream/validation 等信息；对 native provider route，尤其 Gemini GenAI，会设置 `BifrostContextKeyUseRawRequestBody` 并携带 `RawRequestBody` 到 provider，避免 order-sensitive/provider-native JSON 被无意义重序列化。

UAPI 的同协议实现应尽量贴近这个模式。`NormalizeRequestSameProtocol` 允许多做一层已知客户端噪声清理，例如 Cherry Studio 的字面量 `"[undefined]"`，但清理后仍应保持 native body preservation，不应重建协议 body。如果审查发现 UAPI 和 Bifrost 的同协议 raw-body 保留语义有差异，默认应让 UAPI 向 Bifrost 靠拢，除非有明确记录的 UAPI 特有原因。

请重点核对 Bifrost：

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

请重点核对 UAPI：

- `internal/relay/handler.go` 同协议 HTTP 请求必须走 `NormalizeRequestSameProtocol`，不是 `ConvertRequestWithAdaptor`。
- `internal/relay/ws_bridge.go` 的 WS `response.create` HTTP bridge 同协议也必须走 `NormalizeRequestSameProtocol`。
- `internal/relay/provider/convert/registry.go` 的 `NormalizeRequestSameProtocol` 必须只做 sanitizer + parser validation，不做 emitter rebuild。
- Gemini same-format stream 必须保留 native URL/body 语义，不要注入 OpenAI 风格 JSON `stream` 字段。

## 已知必须保持修复的真实回归

Cherry Studio 会在可选字段里发送字面量字符串 `"[undefined]"`。OpenAI Chat 类 API 可能正常，但以下三类协议入口曾出现实际报错，必须保持修复：

- OpenAI Responses：`/v1/responses`
- Gemini native：`/v1beta/models/{model}:streamGenerateContent?alt=sse`
- Anthropic Messages：`/v1/messages`

测试中必须覆盖：

- Responses：`temperature`、`parallel_tool_calls`、`instructions`、`tools`、嵌套 content 字段可能为 `"[undefined]"`；PDF/file 可使用 `input_file.file_data`。
- Gemini：`generationConfig.maxOutputTokens`、`generationConfig.temperature`、`systemInstruction`、`tools` 可能为 `"[undefined]"`；streaming 由 URL/method 表达，不要注入 OpenAI 风格 JSON `stream` 字段。
- Anthropic：`temperature`、`system`、`cache_control`、`tools` 可能为 `"[undefined]"`。

## 必须先做的源码阅读

在设计和修改前，请先用 `rg --files`、`rg` 等方式列出并阅读相关代码。不要只读少数文件。至少覆盖以下范围。

### 1. 当前 UAPI 项目

重点阅读：

- `internal/relay/request_type.go`
- `internal/relay/handler.go`
- `internal/relay/ws_bridge.go`
- `internal/relay/stream_converter.go`
- `internal/relay/provider`
- `internal/relay/provider/convert`
- `internal/relay/provider/schema`
- `internal/relay/provider/ir`
- `internal/relay/provider/stream`
- 各 provider/channel adaptor
- 下游 OpenAI / Anthropic / Gemini / Claude Code / Codex compatible API 层
- 流式响应处理
- tool call / function call 转换
- 多模态 content part 转换
- usage / billing / quota 统计
- error normalization
- model mapping / channel mapping / provider abstraction
- 当前测试，尤其 conversion、stream、tool、multimodal、usage、error 相关测试

请确认当前是否存在混合中间格式，例如：

- ordered parts 与旧字段并存
- `Content`、`ToolCalls`、`ToolResult`、`ReasoningContent` 等旧视图与新 `Parts` 同时作为事实来源
- schema 与 convert 包职责混乱
- provider types 通过 alias 临时兼容旧类型

如果存在，请优先清理成单一、明确、长期可维护的 IR。

### 2. Bifrost

必须完整阅读与协议桥接相关代码，不要只看 schema。

重点包括：

- `upstream/bifrost/core/schemas`
- `upstream/bifrost/core/providers`
- `upstream/bifrost/core/internal/llmtests`
- responses/chat/completion schema
- provider request/response 生命周期
- stream/non-stream 统一处理
- raw request/response、extra params、extra fields
- tool schema order preservation
- prompt caching、thinking、file/image/tool tests

参考重点：

- provider 与 request type 分离
- union schema 表达能力
- `RawRequestBody` / `ExtraParams` / `ExtraFields`
- 原始字段保留
- 同协议 raw-body preservation 的 parse/classify/validate + raw body forwarding 模式
- 多协议测试矩阵
- stream chunk index 与 observability

### 3. Codex 官方源码

必须阅读 Codex 官方实现中与 Responses API、OAuth、SSE、tool、错误、usage 相关代码。

重点包括：

- `upstream/codex/codex-rs/codex-client/src`
- `upstream/codex/codex-rs/codex-api/src`
- `upstream/codex/codex-rs/protocol/src/models.rs`
- `upstream/codex/codex-rs/core/src/stream_events_utils.rs`
- `upstream/codex/codex-rs/core/tests/suite`
- `upstream/codex/codex-rs/*/tests/*sse*`
- request construction
- provider config
- api bridge
- SSE parser
- Responses API event lifecycle
- `function_call`
- `function_call_output`
- `reasoning.encrypted_content`
- usage-limit / quota / context-length / policy errors
- request id / upstream headers
- terminal context-length error handling

结论要求：

Codex 渠道应按 Responses API 思维建模，而不是强行压成 Chat Completions。IR 必须能表达 Responses output item、content part lifecycle、function call output、encrypted reasoning、compaction/context items 等信息。

### 4. Gemini CLI 官方源码

必须阅读 Gemini CLI 官方 Code Assist / Gemini 请求链路。

重点包括：

- `upstream/gemini-cli/packages/core/src/code_assist`
- `upstream/gemini-cli/packages/core/src/core`
- `upstream/gemini-cli/packages/core/src/utils`
- auth / oauth / quota / google error 相关代码
- content converter
- generateContent / streamGenerateContent
- tool handling
- thought handling
- SSE handling

重点确认：

- Code Assist endpoint：
  - `https://cloudcode-pa.googleapis.com/v1internal:generateContent`
  - `:streamGenerateContent?alt=sse`
- 外层 envelope：
  - `model`
  - `project`
  - `user_prompt_id`
  - `request`
  - `enabled_credit_types`
- 内层 request：
  - `contents`
  - `systemInstruction`
  - `cachedContent`
  - `tools`
  - `toolConfig`
  - `labels`
  - `safetySettings`
  - `generationConfig`
  - `session_id`
- generationConfig 完整字段：
  - `temperature`
  - `topP`
  - `topK`
  - `maxOutputTokens`
  - `candidateCount`
  - `stopSequences`
  - `responseLogprobs`
  - `logprobs`
  - `presencePenalty`
  - `frequencyPenalty`
  - `seed`
  - `responseMimeType`
  - `responseSchema`
  - `responseJsonSchema`
  - `routingConfig`
  - `modelSelectionConfig`
  - `responseModalities`
  - `mediaResolution`
  - `speechConfig`
  - `audioTimestamp`
  - `thinkingConfig`

Gemini SSE 必须正确处理多行 `data:`、增量 candidate、tool call、usage、block reason、finish reason。

### 5. Claude Code 官方源码

必须系统阅读 Claude Code 相关源码，尤其 OAuth、Anthropic headers、tool、thinking、MCP、stream、teleport/remote 逻辑。

重点包括：

- `upstream/claude-code-source-1/src`
- `upstream/claude-code-source-2/src`
- `upstream/claude-code-source-3/src`
- `upstream/claude-code-source-3/packages`
- `teleport`
- auth / oauth
- anthropic request headers
- stream/SSE
- tool use / tool result
- thinking / redacted thinking
- MCP tool behavior
- model and beta header logic

重点确认：

- `anthropic-version`
- `anthropic-beta`
- OAuth token 文件、刷新、session/account metadata
- Claude Code 专用 beta，例如 oauth、interleaved-thinking、context-management、prompt-caching、redact-thinking、token-efficient-tools 等
- tool name rewriting / built-in tool handling
- system/developer instruction 真实组织方式
- stream event 与错误结构

### 6. CLIProxyAPI / cliproxyapi 竞品实现

必须完整阅读协议转换相关代码，不要只看 Antigravity 一两个文件。

重点包括：

- `upstream/CLIProxyAPI/internal/translator`
- `upstream/CLIProxyAPI/internal/runtime/executor`
- `upstream/CLIProxyAPI/internal/auth`
- `upstream/CLIProxyAPI/internal/thinking`
- `upstream/CLIProxyAPI/internal/cache`
- `upstream/CLIProxyAPI/internal/registry`
- `upstream/CLIProxyAPI/internal/api`
- `upstream/CLIProxyAPI/internal/runtime/executor/helps`
- cockpit-tools 内嵌 sidecar：
  - `upstream/cockpit-tools/sidecars/cockpit-cliproxy/cdk/CLIProxyAPI/internal`

重点参考：

- OpenAI ↔ Claude
- OpenAI ↔ Gemini
- Gemini ↔ Claude
- Codex ↔ Claude
- Codex ↔ Gemini
- Gemini CLI ↔ OpenAI
- Antigravity ↔ OpenAI / Claude / Gemini
- Responses API ↔ Chat Completions
- stream event reconstruction
- tool call / tool result grouping
- usage helper
- error mapping
- OAuth refresh
- account/session metadata
- thought signature cache

Antigravity 重点确认：

- `cloudcode-pa.googleapis.com`
- daily/sandbox/prod endpoint fallback
- `/v1internal:generateContent`
- `/v1internal:streamGenerateContent`
- `/v1internal:countTokens`
- HTTP/1.1 / Node-like behavior
- OAuth client id/secret
- `project`
- `enabled_credit_types`
- quota / credit retry heuristics
- `thoughtSignature`
- `skip_thought_signature_validator`
- function call / function response grouping
- Claude thinking signature 到 Gemini thoughtSignature 的转换
- Gemini `googleSearch` 与 `functionDeclarations` 的冲突规避
- `thinkingBudget` vs `thinkingLevel`

### 7. new-api

必须阅读 new-api 中与兼容协议、relay、DTO、usage、错误、Codex/OAuth 相关代码。

重点包括：

- `upstream/new-api/dto`
- `upstream/new-api/relay/channel/openai`
- `upstream/new-api/relay/channel/claude`
- `upstream/new-api/relay/channel/gemini`
- `upstream/new-api/relay/channel/codex`
- `upstream/new-api/relay/common`
- `upstream/new-api/service/openaicompat`
- `upstream/new-api/service`
- `upstream/new-api/types`
- `upstream/new-api/controller/codex_oauth.go`
- `upstream/new-api/service/codex_oauth.go`

重点参考：

- Claude adaptor 对 `anthropic-beta` 的透传
- `anthropic-version: 2023-06-01`
- OpenAI Responses ↔ Chat compatibility
- Gemini DTO 同时接受 snake_case 与 camelCase
- Gemini streaming detection：`alt=sse` 或 `streamGenerateContent`
- Gemini native DTO 对 function response 额外字段的保留：
  - `willContinue`
  - `scheduling`
  - `parts`
  - `id`
- usage patch
- error response normalization
- billing / quota 统计

### 8. Antigravity-Manager 与 cockpit-tools

由于 Antigravity 缺少官方源码，必须阅读竞品和管理工具中与真实运行行为相关的代码/文档。

重点包括：

- `upstream/Antigravity-Manager/src`
- `upstream/Antigravity-Manager/src-tauri`
- `upstream/Antigravity-Manager/docs`
- `upstream/Antigravity-Manager/README.md`
- `upstream/Antigravity-Manager/README_EN.md`
- `upstream/cockpit-tools/src/utils/antigravity*`
- `upstream/cockpit-tools/src/services/antigravity*`
- `upstream/cockpit-tools/src/types/gemini.ts`
- `upstream/cockpit-tools/src/types/codex.ts`
- account / quota / model / runtime target 相关代码

重点参考：

- account quota model
- model normalization
- endpoint fallback
- token refresh
- project id 注入
- 403/429/5xx 处理
- quota protection
- thoughtSignature 历史问题
- Codex SSE event lifecycle 补全经验
- image / text quota 区分
- model alias 与 high/low thinking variants

## 当前重构目标

请把 UAPI 当前自定义中间转换层彻底重构为真正协议中立、高保真、可扩展的 IR。

不要让 IR 成为 OpenAI Chat、Anthropic Messages、Gemini GenerateContent、Codex Responses 中任意一个协议的简单变体。

建议设计新包，例如：

`internal/relay/provider/ir`

也可以根据项目结构选择更合适位置，但必须职责清晰。

## 新 IR 必须具备的能力

### Request

应能表达：

- source protocol
- target protocol
- requested model
- routed provider/channel/model
- raw request body
- request headers 中需要保留的协议字段
- metadata
- generation config
- safety settings
- cache settings
- tool choice
- response format / json schema
- stream flag
- provider-specific passthrough fields

### Conversation / Turn / Message

必须使用有序结构作为唯一事实来源，例如：

- `Request.Turns`
- `Turn.Role`
- `Turn.Items`

不要再让 `Content`、`ToolCalls`、`ToolResult`、`ReasoningContent` 等旧字段与 ordered parts 并列成为事实来源。

角色至少覆盖：

- system
- developer
- user
- assistant
- tool
- function
- model
- unknown / provider-specific

### Item / Content Part

必须支持有序 union item，至少包括：

- text
- image
- audio
- video
- file
- document
- tool_use
- tool_result
- function_call
- function_call_output
- reasoning
- thinking
- redacted_thinking
- encrypted_reasoning
- refusal
- citation
- web_search_result
- executable_code
- code_execution_result
- cache_marker
- safety_block
- opaque/native item

每个 item 应支持：

- stable id
- call id
- tool/function name
- arguments raw JSON
- result content
- content type / mime type
- file id / URL / inline bytes / data URI
- provider native type
- provider raw JSON
- unknown fields
- loss records

### Native Preservation

必须设计统一 native/raw 保留机制，例如：

- `NativeEnvelope`
- `SourceProtocol`
- `Raw`
- `UnknownFields`
- `OriginalIndex`
- `ProviderMetadata`

同协议往返时，应优先按 Bifrost 的生产验证模式 replay raw/native body 或 raw/native item，避免无意义重序列化导致信息损失。只有在需要清理已知客户端噪声、补必要路由字段或通过同协议 parser validation 时，才允许对 body 做最小必要修改。

### Loss Accounting

任何不可逆转换都不能静默丢失。必须记录：

- source path
- target path
- loss kind
- severity
- explanation
- original value hash 或摘要
- 是否已通过 metadata/opaque item 保留

例如：

- Claude `cache_control` 转 OpenAI 时不可表达
- Anthropic redacted thinking 转 Gemini 时只能保留为 opaque/encrypted metadata
- Gemini safety block reason 转 OpenAI finish_reason 时语义不等价
- Codex `function_call_output.output` 的结构化 content item 被 Chat Completions string 化
- Gemini `thoughtSignature` 缺失、伪造或回填
- OpenAI Responses item lifecycle 被 Chat delta 压平

### Usage

Usage 必须比当前更细，至少支持：

- input tokens
- output tokens
- total tokens
- prompt tokens
- completion tokens
- cache read tokens
- cache write tokens
- reasoning tokens
- audio tokens
- image tokens
- text tokens
- tool tokens
- provider raw usage
- billing usage
- estimated usage 标记

不能因为协议字段名不同就丢失明细。

### Finish / Stop / Block Reason

必须显式区分：

- OpenAI `finish_reason`
- Anthropic `stop_reason`
- Gemini `finishReason`
- Codex Responses status / incomplete reason
- safety block / moderation block
- content filter
- max token
- tool call
- stop sequence
- error interruption

### Streaming

必须设计协议中立的 stream event IR，支持：

- message start
- content part start
- content delta
- content part end
- tool call start
- tool argument delta
- tool call end
- reasoning start/delta/end
- usage delta
- safety/block event
- error event
- message end
- response completed
- done

并能映射到：

- OpenAI Chat Completions SSE
- OpenAI Responses SSE
- Anthropic Messages SSE
- Gemini SSE
- Claude Code SSE/stream
- Codex Responses SSE

Codex Responses SSE 必须支持完整 lifecycle：

- `response.created`
- `response.output_item.added`
- `response.content_part.added`
- `response.output_text.delta`
- `response.content_part.done`
- `response.output_item.done`
- `response.completed`
- error events

不能只拼 `delta` 文本。

## 实施要求

请直接修改代码，不要只写建议。

建议阶段：

1. 建立新 IR 类型。
2. 为现有协议建立 adapter：
   - OpenAI Chat → IR
   - IR → OpenAI Chat
   - OpenAI Responses → IR
   - IR → OpenAI Responses
   - Anthropic Messages → IR
   - IR → Anthropic Messages
   - Gemini GenerateContent → IR
   - IR → Gemini GenerateContent
   - Gemini CLI / Code Assist → IR
   - IR → Gemini CLI / Code Assist
   - Codex Responses → IR
   - IR → Codex Responses
   - Claude Code OAuth/native → IR
   - IR → Claude Code OAuth/native
   - Antigravity → IR
   - IR → Antigravity
3. 逐步替换旧 `convert.InternalRequest` 或将其降级为过渡 shim。
4. provider adaptor 只依赖新 IR，不再依赖协议特定拼接结构。
5. 删除旧转换层中过时字段、重复函数、临时 alias、无用 schema、死代码。
6. 更新 tests。
7. 跑完整测试或至少相关测试。

如果一次无法完全迁移所有 provider，请先完成核心 IR、主要协议 adapter 和必要过渡 shim，但必须保证新架构方向明确，旧代码不得继续扩张。产品尚未上线，不需要为了历史兼容保留长期包袱。

## 必须删除的旧代码类型

重构后请主动清理：

- 已被新 IR 取代的旧 InternalRequest / InternalMessage / ContentPart 类型
- 只为旧格式服务的重复 converter
- 不再被引用的 helper
- 旧字段兼容 alias
- 双事实来源字段
- 同一协议多套重复转换函数
- 已无调用方的 stream parser
- 无用 schema wrapper
- 重复 model mapping
- 临时 debug/test-only 代码
- 过时注释和误导性文档

删除前请用 `rg` 确认引用。不要保留“以后可能用”的死代码。

## 测试要求

必须补充或更新测试，至少覆盖：

- OpenAI Chat ↔ IR
- OpenAI Responses ↔ IR
- Anthropic Messages ↔ IR
- Gemini GenerateContent ↔ IR
- Gemini CLI / Code Assist ↔ IR
- Codex Responses ↔ IR
- Claude Code OAuth/native ↔ IR
- Antigravity ↔ IR
- same-protocol roundtrip raw preservation
- Bifrost-style same-protocol parse/classify/validate + raw body forwarding
- Cherry Studio `"[undefined]"` 三协议入口回归：
  - `/v1/responses`
  - `/v1beta/models/{model}:streamGenerateContent?alt=sse`
  - `/v1/messages`
- cross-protocol loss records
- tool call id 与 tool result 关联
- function call 与 tool call 差异
- streaming lifecycle
- Codex Responses SSE lifecycle
- Anthropic SSE events
- Gemini multi-line SSE data
- multimodal input：
  - image URL
  - image data URI
  - file id
  - inline file bytes
  - document/PDF
  - audio
- reasoning/thinking：
  - Anthropic thinking
  - redacted thinking
  - Codex encrypted reasoning
  - Gemini thoughtSignature
- cache control / ephemeral cache
- safety settings / block reason
- usage：
  - prompt/completion
  - input/output
  - cache read/write
  - reasoning tokens
  - estimated usage
- error normalization：
  - auth error
  - quota / rate limit
  - context length
  - content filter / safety block
  - upstream 5xx
  - malformed stream

测试中要包含不可逆转换场景，并断言 loss records 存在。

## 输出要求

完成后请给出清晰审查和重构报告，至少包括：

1. 你完整阅读了哪些当前项目文件和 upstream 文件/目录
2. 当前转换层的关键问题
3. 每个问题的风险
4. 与官方客户端、Bifrost、CLIProxyAPI、new-api、Antigravity-Manager、cockpit-tools 的主要差异
5. 新 IR 的设计说明
6. 同协议路径如何参考 Bifrost 的 parse/classify/validate + raw body preservation
7. 哪些地方会发生协议信息损失
8. 如何通过 raw/native/metadata/loss records 保留信息
9. 实际修改了哪些文件
10. 删除了哪些过时/无用/冗余代码
11. 新增/修改了哪些测试
12. 执行了哪些测试命令，结果如何
13. 剩余风险和后续建议

## 工作方式约束

- 不要破坏用户已有未提交修改。
- 不要使用 destructive git 命令。
- 不要回滚与本任务无关的修改。
- 使用 `rg` / `rg --files` 搜索。
- 手工修改文件优先使用 `apply_patch`。
- 如果发现现有设计明显错误，优先改成长期正确架构。
- 不要为了短期兼容保留混乱抽象。
- 不要静默丢失协议字段。
- 不要只做文档审查，必须实现重构和测试。
