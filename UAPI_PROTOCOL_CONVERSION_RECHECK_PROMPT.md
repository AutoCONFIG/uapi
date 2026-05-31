# UAPI 协议转换层架构验收与重构复核提示词

你将在当前 UAPI 项目中继续完成协议转换层的源码驱动验收与架构级修复。

项目路径：

```text
/media/yun/de2a43ce-446c-4a62-99b3-8ddc6ea1ef87/UAPI
```

当前产品尚未正式上线生产，不需要保留历史兼容包袱。旧中间结构、旧 alias、重复 converter、临时 shim、死代码、双事实来源如果不符合长期架构目标，应删除或迁移。

重要约束：

- 不要破坏用户已有未提交改动。
- 不要使用 destructive git 命令。
- 不要凭记忆或字段名猜测协议行为；每个关键功能点都应参考当前源码和 upstream 真实实现。
- 本提示词中的已知事实可能继续过时，一切以当前 `git diff`、当前源码、当前 upstream 代码为准。

## 开始前必须做

先执行并审查：

```bash
git status --short
git diff --stat
git diff
```

目的：

- 识别当前真实改动范围。
- 区分已有修复、用户改动、无关修改、生成噪音。
- 不回滚用户已有改动，但要在报告中指出可疑或无关改动。

当前已知的无关/用户改动可能包括 UI/settings/login 相关文件和 `web/public/` 资源。必须重新以 `git status --short` 为准，不要擅自回滚：

- `internal/admin/settings_handler.go`
- `internal/appsettings/settings.go`
- `web/app/admin/settings/page.tsx`
- `web/app/globals.css`
- `web/app/login/page.tsx`
- `web/app/page.tsx`
- `web/components/login-form.tsx`
- `web/components/theme-provider.tsx`
- `web/types/api.ts`
- `web/public/`

## 当前已知事实，需要重新验证

这些是生成本提示词时的工作区状态摘要，必须重新用源码验证：

- 同协议 HTTP 路径应在 `internal/relay/handler.go` 调用 `NormalizeRequestSameProtocol`。
- WS `response.create` HTTP bridge 应在 `internal/relay/ws_bridge.go` 同协议时调用 `NormalizeRequestSameProtocol`。
- `NormalizeRequestSameProtocol` 位于 `internal/relay/provider/convert/registry.go`，目标行为是只做 `"[undefined]"` sanitizer + 协议 parser validation，并返回清理后的 native body，不通过 IR emitter rebuild。
- `requestDraft` / `responseDraft` / `requestTurnDraft` / `requestItemDraft` / `responseChoiceDraft` 在上一轮已被删除，但必须重新搜索确认。
- `adapterRequest` / `adapterResponse` 在上一轮搜索应为 0，但必须重新搜索确认。如果仍存在，不能简单结论为“内部层所以没问题”。
- 生产转换入口应直接走 IR parser/emitter，重点看 `internal/relay/provider/convert/internal.go`。
- stream 去重逻辑应在 `internal/relay/provider/stream/ir_anthropic_gemini.go`，通过 `streamedText` 避免 Responses delta 后 `done/completed` 再向 Anthropic/Gemini 下游重复输出完整文本。
- Responses SSE function call done arguments 已修过，重点复核 `internal/relay/provider/stream/ir_openai.go` 是否完整处理 `response.output_item.done` 中的 `arguments`，并避免与 `response.function_call_arguments.delta` 重复拼接。
- cache creation/read 字段应已存在于 `internal/db/log.go`、`internal/db/usage_event.go`、`internal/user/dto.go`、`internal/admin/internal_relay_handler.go`。

## 核心目标

检查并修复协议转换层是否真正达到长期架构目标：

- 建立协议中立、高保真、可扩展的 IR。
- ordered item 是唯一语义事实来源。
- 消除旧转换层的双事实来源、重复 converter、临时 shim、死代码。
- 覆盖 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini GenerateContent、Gemini CLI / Code Assist、Claude Code、Codex Responses、Antigravity。
- 保留 raw/native metadata。
- 显式记录不可逆转换 loss。
- stream、tool call、多模态、reasoning/thinking、usage、cache hit、billing、quota、错误处理有测试闭环。
- provider-specific request shaping 只在 provider/adaptor 层发生。

## 协议格式判断规则

必须遵守：

- downstream/client format 由 HTTP path / request type 判断，不由模型名判断。
- upstream format 由 channel `Type` + `APIFormat` 判断，不由模型名判断。
- 模型名只参与 channel/account/model 路由和模型映射。

期望请求流：

1. 从 HTTP path/request type 识别 downstream/client format：
   `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、Gemini `generateContent` / `streamGenerateContent`、media endpoint、WS bridge event。
2. 从 channel `Type` + `APIFormat` 识别 upstream format：
   `openai` + `standard|responses|codex`，`anthropic` + `standard|claude_code`，`gemini` + `standard|gemini_code`，或 `antigravity`。
3. 如果 client format == upstream format，调用 `NormalizeRequestSameProtocol`：
   清理 JSON 中字面量 `"[undefined]"`，用该协议 parser 校验，然后转发清理后的原 body，不通过 IR emitter 重建。
4. 如果格式不同，parse 到协议中立 IR，prepare target，再通过 adaptor/converter emit。
5. provider-specific shaping 只应在 provider/adaptor 层发生。

## 必须优先参考 upstream

不要凭字段名猜测，必须阅读真实 upstream 实现。

### Bifrost raw-body preservation

重点阅读：

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

UAPI 同协议实现应尽量贴近 Bifrost：

- parse/classify/validate 之后保留 native body。
- 不做 emitter rebuild。
- Gemini same-format stream 不注入 OpenAI 风格 JSON `stream` 字段。
- Cherry Studio `"[undefined]"` 清理后仍保持 raw-body preservation。

### Bifrost / upstream stream conversion

重点阅读：

- `upstream/bifrost/core/providers/gemini/responses.go`
  - Responses stream -> Gemini stream state machine
  - `ResponsesStreamResponseTypeOutputTextDone`
  - 避免 delta 后 done/completed 重复输出的逻辑
- `upstream/bifrost/transports/bifrost-http/integrations/router.go`
  - streaming request handling
- `upstream/CLIProxyAPI/internal/translator`
  - OpenAI / Claude / Gemini / Codex stream 转换与 usage 映射

要求：

- 首个 provider content event 应立即转换输出，不能等最终 response。
- Responses delta 后 `done/completed` 不得让 Anthropic/Gemini 下游重复输出完整文本。
- Gemini SSE 多行 `data:` 必须正确处理。

### 官方客户端协议

至少搜索并阅读：

- `upstream/codex/codex-rs/codex-client/src`
- `upstream/codex/codex-rs/codex-api/src`
- `upstream/codex/codex-rs/protocol/src/models.rs`
- `upstream/codex/codex-rs/core/src/stream_events_utils.rs`
- `upstream/gemini-cli/packages/core/src/code_assist`
- `upstream/gemini-cli/packages/core/src/core`
- `upstream/gemini-cli/packages/core/src/utils`
- `upstream/claude-code-source-*`
- `upstream/Antigravity-Manager/src`
- `upstream/cockpit-tools/src/utils/antigravity*`
- `upstream/cockpit-tools/src/services/antigravity*`
- `upstream/new-api/dto`
- `upstream/new-api/relay/channel/openai`
- `upstream/new-api/relay/channel/claude`
- `upstream/new-api/relay/channel/gemini`
- `upstream/new-api/relay/channel/codex`
- `upstream/new-api/service`

## 第一阶段输出

先输出一个短但高密度的审查汇总，然后继续修复，不要停在建议。

必须包含：

1. 当前重构完成度。
2. 是否仍存在旧转换层残留。
3. 是否仍存在双事实来源。
4. 哪些 protocol adapter 覆盖不足。
5. 哪些信息仍可能静默丢失。
6. 哪些测试缺失。
7. 哪些代码应删除但未删除。
8. 哪些问题必须立即修复。

## 必须阅读的 UAPI 范围

先用 `rg --files` 和 `rg` 梳理，不要抽样。

至少覆盖：

- `internal/relay/request_type.go`
- `internal/relay/handler.go`
- `internal/relay/ws_bridge.go`
- `internal/relay/ws_handler.go`
- `internal/relay/ws_proxy.go`
- `internal/relay/stream_converter.go`
- `internal/relay/streaming.go`
- `internal/relay/billing.go`
- `internal/relay/usage_estimator.go`
- `internal/relay/provider`
- `internal/relay/provider/convert`
- `internal/relay/provider/schema`
- `internal/relay/provider/ir`
- `internal/relay/provider/stream`
- `internal/db/log.go`
- `internal/db/usage_event.go`
- `internal/user/service.go`
- `internal/user/dto.go`
- `internal/admin/internal_relay_handler.go`
- `internal/quota`

## IR 架构验收

确认 IR 能表达并真正用于转换：

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
- usage details / cache read / cache creation / estimated usage

重点搜索：

```text
InternalRequest
InternalMessage
InternalResponse
adapterRequest
adapterResponse
requestDraft
responseDraft
requestTurnDraft
requestItemDraft
responseChoiceDraft
Content
ToolCalls
ToolResult
ReasoningContent
ContentPart
Parts
deprecated
legacy
shim
alias
```

分类判断：

- 合理 schema 类型
- 测试 fixture
- 临时迁移层
- 生产路径旧事实来源
- 死代码

生产路径旧事实来源和死代码应删除或迁移。

如果 `adapterRequest` / `adapterResponse` / `requestDraft` / `responseDraft` 仍存在，最终报告必须说明：

- 为什么还存在。
- 是否仍在生产路径。
- 是否仍作为事实来源。
- 本轮删除或迁移了什么。
- 剩余部分为什么不能继续删除。
- 还需要哪些具体步骤才能删除。

不能用“只是内部层所以没问题”作为结论。

## Loss accounting

至少覆盖并测试：

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
- provider cache read/write usage
- unknown fields

发现静默丢失时，修复为：

- raw/native metadata
- opaque item
- loss record

## Cache / Usage / Billing / Quota 专项

必须沿完整链路追踪：

1. provider response usage parse
2. stream final usage aggregation
3. IR usage representation
4. protocol response conversion
5. relay billing settle
6. pre-deduct / refund
7. quota policy window
8. db log / usage event
9. user usage API
10. admin/internal usage event

验收规则：

- cache hit 不能从请求里的 `cache_control`、`cachedContent`、prompt cache marker 推断。
- cache hit 必须来自 provider response usage、stream final usage、trailer usage 或 provider raw metadata。
- 请求 cache marker 只能表示“请求启用缓存”，不能表示“已经命中缓存”。
- 必须区分 cache read / cache hit、cache creation / cache write、normal input、output、total、estimated、unknown。

必须核对字段：

- OpenAI Chat: `usage.prompt_tokens_details.cached_tokens`
- OpenAI Responses: `usage.input_tokens_details.cached_tokens`
- Codex Responses SSE: `response.completed.response.usage.input_tokens_details.cached_tokens`
- Anthropic:
  - `cache_creation_input_tokens`
  - `cache_read_input_tokens`
  - `cache_creation.ephemeral_5m_input_tokens`
  - `cache_creation.ephemeral_1h_input_tokens`
- Gemini:
  - `usageMetadata.cachedContentTokenCount`
  - `cachedContent`
- Gemini CLI / Code Assist internal response usage
- Antigravity internal response usage

必须补齐或确认测试：

- OpenAI Chat cached tokens -> IR -> billing/log。
- OpenAI Responses cached tokens -> Chat usage。
- Codex Responses stream final cached tokens。
- Anthropic flat and nested cache creation/read。
- Gemini non-stream and stream `cachedContentTokenCount`。
- Gemini CLI / Code Assist cached content usage。
- Antigravity cached content usage。
- HTTP relay billing settle uses cache read/write。
- WS relay billing settle uses cache read/write, not fixed zero。
- db log / usage event 保存 cache read/write。
- user usage logs API 返回 cache read/write。
- 不支持目标协议时有 loss record，raw/native usage 不丢。

## Streaming 专项

检查并修复：

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
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.content_part.done`
- `response.output_item.done`
- `response.completed`
- error event

特别复核 Claude Code 通过 Anthropic 协议族调用 GPT 5.5 / OpenAI Responses 协议族时的问题：

- Responses `function_call.done.arguments` 必须能转换成 Anthropic `tool_use` input。
- `description`、`prompt` 等 required 参数不能因为只处理 delta 而丢失。
- 如果 delta 已经输出过一部分 arguments，done 只能补齐缺失 suffix，不能重复输出。
- 这个问题可能不限于 Claude Code 和 OpenAI Responses，其他协议组合也要检查是否有类似的 done/final event 覆盖 delta 的问题。

## Tool / Function / Multimodal

必须检查并测试：

- tool call id 稳定保留。
- tool result 正确关联 tool call。
- OpenAI function call 与 tool call 差异保留。
- Anthropic `tool_use` / `tool_result`。
- Gemini `functionCall` / `functionResponse`。
- Codex `function_call` / `function_call_output`。
- Claude Code OAuth tool name rewrite。
- Antigravity function call/response grouping。
- arguments raw JSON 保留。
- tool schema key order 尽量保留。
- image URL / data URI / inline bytes。
- file id / inline file bytes / document/PDF。
- audio / video。
- mime type / filename / provider file metadata。
- 不允许用单一 text 字段吞多模态内容。

## Cherry Studio 回归

必须保持：

- `/v1/responses`
- `/v1beta/models/{model}:streamGenerateContent?alt=sse`
- `/v1/messages`

这些入口可能出现字面量 `"[undefined]"`：

- Responses: `temperature`、`parallel_tool_calls`、`instructions`、`tools`、嵌套 content、PDF/file `input_file.file_data`
- Gemini: `generationConfig.maxOutputTokens`、`generationConfig.temperature`、`systemInstruction`、`tools`
- Anthropic: `temperature`、`system`、`cache_control`、`tools`

同协议清理后仍必须 raw-body preservation，不可 emitter rebuild。

## 官方客户端类渠道

### Codex

- 按 Responses API 建模。
- request body 字段正确。
- SSE lifecycle 完整。
- `function_call_output`。
- `reasoning.encrypted_content`。
- usage-limit / context-length / policy error。
- request id / upstream header 保留。

### Gemini CLI / Code Assist

- endpoint:
  - `v1internal:generateContent`
  - `v1internal:streamGenerateContent?alt=sse`
- envelope:
  - `model`
  - `project`
  - `user_prompt_id`
  - `request`
  - `enabled_credit_types`
- inner request:
  - `contents`
  - `systemInstruction`
  - `cachedContent`
  - `tools`
  - `toolConfig`
  - `labels`
  - `safetySettings`
  - `generationConfig`
  - `session_id`
- OAuth / quota / Google error 正确。

### Claude Code

- `anthropic-version`
- `anthropic-beta`
- OAuth token/session/account metadata
- beta headers
- tool name rewrite
- thinking/redacted thinking
- prompt caching
- MCP/builtin tools
- stream events

### Antigravity

- `cloudcode-pa.googleapis.com`
- daily/sandbox/prod fallback
- `/v1internal:generateContent`
- `/v1internal:streamGenerateContent`
- `/v1internal:countTokens`
- OAuth client id/secret
- project id 注入
- quota / credit retry
- `thoughtSignature`
- `skip_thought_signature_validator`
- `thinkingBudget` vs `thinkingLevel`
- `googleSearch` 与 `functionDeclarations` 冲突规避
- model normalization
- image/text quota 区分

## 修复要求

发现问题后直接修复：

- 优先修架构性问题。
- 可以大规模重构，因为未上线生产。
- 删除旧代码和冗余代码。
- 不引入新的双事实来源。
- 不通过临时 adapter 掩盖 IR 表达能力不足。
- 不静默丢字段。
- 不只改测试绕过问题。
- 删除前必须用 `rg` 确认引用。
- 不破坏用户已有未提交修改。
- 不使用 destructive git 命令。

## 验证要求

至少运行：

```bash
go test -count=1 ./internal/relay/provider/convert
go test -count=1 ./internal/relay/provider/stream
go test -count=1 ./internal/relay/provider/...
go test -count=1 ./internal/relay
go test -count=1 ./internal/quota
go test -count=1 ./internal/admin
go test -count=1 ./...
git diff --check
```

如部分测试无法运行，说明原因，并运行更小范围替代测试。

## 最终报告

最终输出必须包含：

1. 审查发现的问题清单。
2. 已修复的问题。
3. 删除的旧代码/冗余代码。
4. 修改的关键文件。
5. 新增/修改的测试。
6. 执行的测试命令和结果。
7. 仍需后续处理的风险。
8. 与 upstream 官方/竞品实现对照后的关键结论。

报告必须具体到源码路径。

## 证据矩阵

最终报告给出简短证据矩阵。

每个核心能力至少对应：

- UAPI 实现文件
- upstream 对照依据
- 测试文件
- 是否存在 loss record
- 是否仍有风险

核心能力：

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
- cache control preservation
- cache hit usage extraction
- cache read/write billing
- cache usage DB logging
- error normalization

## 缓存命中项目链路矩阵

最终报告必须额外给出：

| 环节 | UAPI 文件 | upstream 对照 | 当前结论 | 修复内容 | 测试 |
|---|---|---|---|---|---|
| provider usage parse | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| IR usage | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| stream final usage | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| billing settle | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| quota/policy window | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| db log/event | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |
| user/admin API | 具体路径 | 具体 upstream 路径 | pass/fail | 具体修改 | 测试路径 |

## 不允许的结论

不要用这些话结束任务：

- “建议后续重构”
- “可以考虑删除”
- “暂时保留兼容”
- “adapter 只是内部层所以没问题”

如果可以删除旧结构，就直接删除或迁移。如果不能继续删除，必须给出源码级阻塞原因和下一步具体文件级计划。
