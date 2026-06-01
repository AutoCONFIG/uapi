# UAPI 协议转换层架构文档

本文档详细描述 UAPI 项目的协议转换层架构，涵盖四大协议族（OpenAI Chat Completions、OpenAI Responses、Gemini/Google GenAI、Anthropic Messages）的完整转换逻辑、认证体系、网关路由和 passthrough 决策机制。

---

# upstream/bifrost 四大协议转换层审计

本文只整理 `D:\UAPI\upstream\bifrost` 中四类上游兼容协议的转换实现：OpenAI Chat Completions、OpenAI Responses、Gemini / Google GenAI、Anthropic Messages。Bedrock、Cohere、文件、batch、音频、图像、container 等实现只在影响这四类协议边界时提及。

核心结论：Bifrost 的 HTTP integration 不直接拼 provider JSON，而是把上游协议请求先解析为协议专属 request struct，再转成内部 `BifrostRequest` 子结构，交给 core 调度、插件、路由、fallback、retry，最后由目标 provider adapter 转成目标 wire request。响应方向反向执行：provider typed/raw response -> Bifrost typed response/stream chunk -> integration converter -> 上游兼容 JSON/SSE。

```text
HTTP route
  -> integration request struct
  -> BifrostChatRequest / BifrostResponsesRequest / CountTokensRequest
  -> core dispatch, plugins, routing, fallback, retry
  -> provider adapter wire request
  -> provider typed/raw response or stream chunks
  -> integration response converter
  -> upstream-compatible JSON/SSE
```

四类协议并不共用同一个内部主协议：

- OpenAI Chat Completions: `OpenAIChatRequest -> BifrostChatRequest -> provider.ChatCompletion(...)`
- OpenAI Responses: `OpenAIResponsesRequest -> BifrostResponsesRequest -> provider.Responses(...)`
- Anthropic Messages: `AnthropicMessageRequest -> BifrostResponsesRequest -> provider.Responses(...)`
- Gemini `generateContent`: `GeminiGenerationRequest -> BifrostResponsesRequest -> provider.Responses(...)`

因此 Anthropic Messages 和 Gemini GenAI 不是先降级到 Chat，再转给 provider；它们主线都是内部 Responses 格式。跨协议丢字段、降级和过滤主要发生在 provider adapter、协议响应 converter、raw passthrough sanitizer 以及目标 provider capability gate。

## 1. 通用路由层

主要源码：

- `upstream/bifrost/transports/bifrost-http/integrations/router.go`
- `upstream/bifrost/transports/bifrost-http/integrations/utils.go`
- `upstream/bifrost/transports/bifrost-http/integrations/openai.go`
- `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go`
- `upstream/bifrost/transports/bifrost-http/integrations/genai.go`
- `upstream/bifrost/core/schemas/chatcompletions.go`
- `upstream/bifrost/core/schemas/responses.go`
- `upstream/bifrost/core/providers/openai/chat.go`
- `upstream/bifrost/core/providers/openai/responses.go`
- `upstream/bifrost/core/providers/openai/openai.go`
- `upstream/bifrost/core/providers/anthropic/responses.go`
- `upstream/bifrost/core/providers/anthropic/requestbuilder.go`
- `upstream/bifrost/core/providers/anthropic/anthropic.go`
- `upstream/bifrost/core/providers/gemini/responses.go`
- `upstream/bifrost/core/providers/gemini/gemini.go`
- `upstream/bifrost/core/providers/gemini/utils.go`

`RouteConfig` 定义每个 endpoint 的协议行为：`Path`、`Method`、`GetHTTPRequestType`、`GetRequestTypeInstance`、可选 `RequestParser`、`PreCallback`、`RequestConverter`、各类 typed response converter、stream converter、error converter、async converter、batch/file/container/cached-content converter 等。`GenericRouter` 统一负责 body parsing、large payload hook、`x-bf-async`/`x-bf-async-id`、`x-bf-passthrough-extra-params`、provider response headers、`x-bifrost-*` 路由结果 headers、stream error、large response reader 和 SSE `[DONE]` 规则。

通用边界：

- 普通 JSON 请求默认 `sonic.Unmarshal`；multipart 或二进制 route 自己提供 `RequestParser`。
- large payload hook 在 body parsing 前运行；命中后跳过 JSON/multipart materialization，只靠 hook 放入的 reader 和 metadata 补足 `model`、`stream`、modalities 等最小信息。
- `extra_params` 不默认抽取，必须显式带 `x-bf-passthrough-extra-params: true`；large payload mode 下不抽取，因为不会 materialize body。
- async retrieve 用 `x-bf-async-id`，在 body parsing 前短路；async create 用 `x-bf-async`，必须先完成 request conversion。streaming + async 会被拒绝。
- async 只支持 inference 中的 Chat 或 Responses，并且 route 必须配置对应 async converter。当前 OpenAI Responses、Anthropic Messages 配了 `AsyncResponsesResponseConverter`；OpenAI Chat route 没有 `AsyncChatResponseConverter`，所以 OpenAI Chat async 会报不支持。
- route 成功响应会附加 provider raw headers 和 `x-bifrost-provider`、`x-bifrost-original-model`、`x-bifrost-resolved-model`、`x-bifrost-request-type`，fallback 命中时还有 `x-bifrost-fallback-index`。
- 普通 SSE 默认追加 `data: [DONE]`；判断分为两层：
  - 第一层（按 route Type 或 Path）：Anthropic route、路径包含 `/responses` 的 route、OpenAI image generation route 不追加 `[DONE]`
  - 第二层（额外排除）：GenAI route 和 Bedrock route 也不追加 `[DONE]`
  - 综上：Anthropic Messages、OpenAI Responses、OpenAI Image Generation、Gemini/GenAI、Bedrock 这五类都不追加 `[DONE]`
- `RawRequestBody` / `UseRawRequestBody` 是“复用原始 body 的 provider 优化”，不是透明代理。provider 仍会改 path/header/model，可能补默认字段、删除不支持字段、注入 beta header、合并/删除 `fallbacks`、执行 sanitizer。
- 同协议 raw-first 只在 provider、endpoint、raw response、passthrough 条件都满足时启用；跨协议一定走 typed conversion，无法保证无损。

## 2. OpenAI Chat Completions

### 2.1 上游入口

`CreateOpenAIRouteConfigs(pathPrefix, ...)` 注册：

| Method | Path suffix | RequestType | Request struct |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | `chat_completion` | `openai.OpenAIChatRequest` |
| `POST` | `/chat/completions` | `chat_completion` | `openai.OpenAIChatRequest` |
| `POST` | `/openai/deployments/{deploymentPath:*}` | 按 path suffix 动态判定 | 多种 OpenAI request struct |

独立 OpenAI integration 的 `pathPrefix` 是 `/openai`，所以表里的 suffix 会再加 `/openai` 前缀：实际 chat 入口包括 `/openai/v1/chat/completions`、`/openai/chat/completions`，Azure 兼容 deployment 入口实际为 `/openai/openai/deployments/{deploymentPath:*}`。该动态 route 会在 `{deploymentPath}` 中找到 `chat`、`responses`、`completions`、`embeddings`、`audio`、`images`、`models` 等 endpoint 起点；起点前的部分作为 deployment/provider+deployment 写回 `model`。

### 2.2 请求解析和内部格式

- body 默认解析为 `OpenAIChatRequest`；large payload 命中时跳过解析，由 `openAILargePayloadPreHook` 从 metadata 回填 `model` 和 `stream`。
- `ToBifrostChatRequest(ctx)` 使用 `ParseModelString(req.Model, defaultProvider)`；默认 provider 是 OpenAI，也可被 header、model prefix、model catalog、routing rule 影响。
- `messages` 经 `ConvertOpenAIMessagesToBifrostMessages` 转为 `[]schemas.ChatMessage`。
- `ChatParameters` 直接挂到 `BifrostChatRequest.Params`，包括 `tools`、`tool_choice`、`response_format`、`reasoning`、`web_search_options`、`prediction`、`service_tier`、`extra_params` 等。
- `fallbacks` 字段会解析为统一 fallback 列表。
- OpenAI message content 支持 string 或 content blocks；assistant message 会保留 `tool_calls`、`reasoning`、`reasoning_details` 等。tool call arguments 仍是字符串，不保证是合法 JSON。

### 2.3 下游 OpenAI-compatible adapter

`ToOpenAIChatRequest(ctx, bifrostReq)`：

- 内部 message 转回 OpenAI `messages`。
- `max_completion_tokens` 小于 OpenAI 最小值时提升到 `MinMaxCompletionTokens`。
- `user` 超过 OpenAI 64 字符限制时清理。
- function tool 的 `parameters` 会 `Normalized()`，稳定 JSON key 顺序，减少 prompt cache 因 key 顺序漂移失效。
- OpenAI Chat request 的 JSON marshal 阶段还会剥离跨协议残留：content block 上的 Anthropic `cache_control`、`citations`、file block 的 `file_type`/`file_url`，以及 tool 上的 Anthropic server-tool shape、`cache_control`、`defer_loading`、`allowed_callers`、`input_examples`、`eager_input_streaming`。这些字段即使进入了内部 Chat 参数，也不会原样发给 OpenAI。
- OpenAI/Azure 保留 OpenAI chat 形态，并把 `reasoning.max_tokens` 按 output budget 估算成 effort 后清空 `max_tokens`；已有 `reasoning.effort` 则 normalize effort。
- 非 OpenAI-compatible provider 会过滤 OpenAI 专有参数：`prediction`、`prompt_cache_key`、`prompt_cache_retention`、`verbosity`、`store`、`web_search_options`。
- Fireworks 特例：`prompt_cache_key` 映射为 `prompt_cache_isolation_key`；`prediction` 会在通用过滤前临时保存再恢复。
- Gemini 特例：过滤 OpenAI 专有参数后额外清空 `service_tier`。
- Mistral 特例：`max_completion_tokens` 改为 `max_tokens`；struct tool choice 降为 `"any"`；reasoning effort 只保留 `none` 或 `high`。
- Vertex Mistral 会走 Mistral 兼容处理。
- xAI Grok reasoning 模型会过滤 `presence_penalty`；部分模型过滤 `frequency_penalty`、`stop` 或 `reasoning.effort`。
- custom provider 被标记为自定义时不做通用 OpenAI 专有参数过滤。

### 2.4 响应、stream、错误

- 如果下游 provider 是 OpenAI 且 `ExtraFields.RawResponse != nil`，非流和流都 raw-first 直返。
- typed 非流响应会把 assistant message 中全为 text 的多个 content blocks 合并为一个 string `content`，以匹配 OpenAI SDK 常见形态；非纯文本 block 不合并。
- typed stream 直接输出 OpenAI chat chunk shape；`GenericRouter` 对该类普通 SSE 追加 `[DONE]`。
- error converter 直接返回 `BifrostError`，整体接近 OpenAI/Bifrost error shape。

## 3. OpenAI Responses

### 3.1 上游入口

`CreateOpenAIRouteConfigs` 注册：

| Method | Path suffix | RequestType | Request struct |
|---|---|---|---|
| `POST` | `/v1/responses` | `responses` 或 `responses_stream` | `openai.OpenAIResponsesRequest` |
| `POST` | `/responses` | 同上 | `openai.OpenAIResponsesRequest` |
| `POST` | `/openai/responses` | 同上 | `openai.OpenAIResponsesRequest` |
| `POST` | `/v1/responses/input_tokens` | `count_tokens` | `openai.OpenAIResponsesRequest` |
| `POST` | `/responses/input_tokens` | `count_tokens` | `openai.OpenAIResponsesRequest` |
| `POST` | `/openai/responses/input_tokens` | `count_tokens` | `openai.OpenAIResponsesRequest` |
| `POST` | `/openai/deployments/{deploymentPath:*}` with `/responses` suffix | `responses` / `count_tokens` | `openai.OpenAIResponsesRequest` |

`/openai/responses` 和 `/openai/responses/input_tokens` 是 suffix 级兼容别名；独立 OpenAI integration 下实际分别是 `/openai/openai/responses`、`/openai/openai/responses/input_tokens`。WebSocket Responses 路径另有 `/v1/responses`、`/responses`、`/openai/responses`，由 `transports/bifrost-http/handlers/wsresponses.go` 单独处理，不属于本文 HTTP 主线。

### 3.2 请求解析和内部格式

- body 解析为 `OpenAIResponsesRequest`；large payload 命中时从 metadata 回填 `model` 和 `stream`。
- Responses route 的 `PreCallback` 会记录 User-Agent；如果是 Azure OpenAI SDK User-Agent，会把默认 provider 从 OpenAI 改为 Azure。
- `ToBifrostResponsesRequest(ctx)` 解析 provider/model。`input` 如果不是 array，则把 string input 包成一个 user message。
- `instructions`、`tools`、`tool_choice`、`text.format`、`reasoning`、`metadata`、`parallel_tool_calls`、`include`、`store`、`service_tier` 等保留在 `ResponsesParameters`。
- count tokens route 复用同一个 `ToBifrostResponsesRequest(ctx)`，但放入 `BifrostRequest.CountTokensRequest`，下游走 `/v1/responses/input_tokens`。

### 3.3 下游 OpenAI adapter

`ToOpenAIResponsesRequest(bifrostReq)` 的关键实现：

- compaction content block 转为普通 text block；空 summary 的 compaction block 会跳过，若整条 message 只剩空 compaction 则跳过 message。
- OpenAI 非 `gpt-oss` 模型不接受 reasoning content block：有 summary 时保留 summaries；没有 summary 的 reasoning-only message 会被跳过。
- 非 reasoning OpenAI 模型遇到跨 provider encrypted reasoning，会剥离 `EncryptedContent`，避免把不可解密状态发给 OpenAI。
- OpenAI Responses 不发送 `reasoning.max_tokens`：已有 effort 则 normalize；只有 max_tokens 则按 output budget 估算 effort，然后清空 max_tokens。
- `reasoning.summary == "none"` 是 Anthropic display 语义，发 OpenAI 时清掉。
- OpenAI 非 reasoning 模型会清空整个 `reasoning`。
- OpenAI o1/o3/o4/GPT-5 reasoning 模型通常清 `top_p`；GPT-5.x 在 effort 为空或 `none` 且非 `-pro`/`-codex` 变体时允许保留。
- `max_output_tokens` 小于 OpenAI 最小值时提升到 `MinMaxCompletionTokens`；`user` 也受 64 字符限制。
- function tool schema 会 `Normalized()`。
- `filterUnsupportedTools()` 第一阶段保留：`function`、`file_search`、`computer_use_preview`、`web_search`、`web_fetch`、`mcp`、`code_interpreter`、`image_generation`、`local_shell`、`custom`、`web_search_preview`、`memory`、`tool_search`、`namespace`；其他 Responses tool 丢弃。第二阶段在 `OpenAIResponsesRequest.MarshalJSON()` 里还会把 Anthropic-only 的 `web_fetch`、`memory` 从最终 OpenAI wire body 删除，并剥离 tool 上的 `cache_control`、`defer_loading`、`allowed_callers`、`input_examples`、`eager_input_streaming`。
- `computer_use_preview.enable_zoom` 会剥离；`zoom`/带 `region` 的 computer action 会改写为 OpenAI 可接受形态。
- `web_search.max_uses`、`blocked_domains`、`time_range_filter` 会剥离；只保留 OpenAI 支持的 allowed domains、user location、search context size、external web access、search content types 等。
- Responses message 也有 OpenAI wire sanitizer：message/content/tool-output 上的 `cache_control` 会剥离，file block 的 `file_type` 会剥离，`rendered_content` block 会被过滤，非 OpenAI 原生 annotation 会被过滤；`function_call_output.output` 如果是纯文本 content blocks，会压平成单个字符串，因为 OpenAI Responses wire schema 要求这里是 string。
- reasoning item 发给 OpenAI 时会清掉 `role`；gpt-oss 可把 summary 转回 reasoning content block，非 gpt-oss/非 reasoning 模型会跳过不兼容的 reasoning-only 或 encrypted reasoning 状态。
- `ExtraParams` 从 `ResponsesParameters.ExtraParams` 复制到 OpenAI request；只有显式 passthrough extra params 时才会从上游 JSON 的 `extra_params` 抽取。

### 3.4 响应、stream、async、错误

- 下游 OpenAI raw response 优先直返。
- typed 非流响应返回 `resp.WithDefaults()`，补齐 `object`、`output`、`tools`、`tool_choice`、`text.format`、usage details 等 OpenAI Responses 默认字段。
- typed stream event name 使用 `resp.Type`；返回 `resp.WithDefaults()`。OpenAI Responses stream 不追加 `[DONE]`，按事件/连接结束表示完成。
- async create/retrieve 可用：pending/processing/completed 都返回 Responses-compatible object；completed 时再走普通 Responses converter。
- error converter 直接返回 `BifrostError`。
- OpenAI provider 对 Responses 有 `shouldFallbackResponsesToChat(...)`：如果目标 provider 不支持 Responses 但支持 Chat，可能设置 `BifrostContextKeyIsResponsesToChatCompletionFallback` 并把 `BifrostResponsesRequest.ToChatRequest()` 发到 ChatCompletion。这是明确的跨协议降级路径，Responses 特有 item、server tool lifecycle、部分 reasoning/state 字段可能无法无损表达。

## 4. Anthropic Messages

### 4.1 上游入口

Anthropic integration 的 Messages 主线：

| Method | Path suffix | RequestType | Request struct |
|---|---|---|---|
| `POST` | `/v1/messages` | `responses` 或 `responses_stream` | `anthropic.AnthropicMessageRequest` |
| `POST` | `/v1/messages/{path:*}` | 同上 | `anthropic.AnthropicMessageRequest` |
| `POST` | `/v1/messages/count_tokens` | `count_tokens` | `anthropic.AnthropicMessageRequest` |

独立 Anthropic integration 的 `pathPrefix` 是 `/anthropic`，所以上述实际 HTTP 路径是 `/anthropic/v1/messages`、`/anthropic/v1/messages/{path:*}`、`/anthropic/v1/messages/count_tokens`。同文件还注册 `/v1/complete`、`/v1/models`、files、batch 等；它们不是本文 Messages 主线。`/v1/messages/{path:*}` 主要服务 Claude Code/Claude CLI 扩展 path 和 raw passthrough。

### 4.2 请求解析和内部格式

`AnthropicMessageRequest.ToBifrostResponsesRequest(ctx)`：

- provider/model 经 `ParseModelString`；默认 provider 是 Anthropic。
- `max_tokens` -> `Params.MaxOutputTokens`；`temperature`、`top_p` 直接映射；`top_k`、`stop_sequences`、`speed`、`inference_geo`、`context_management` 进入 `ExtraParams` 或专属字段。
- `metadata.user_id` -> `Params.User`。
- top-level `cache_control` -> `ExtraParams["cache_control"]`。
- `output_format` 和 GA `output_config.format` -> Responses `text.format`。
- `output_config.task_budget` -> `ExtraParams["task_budget"]`。
- `thinking` -> `ResponsesParametersReasoning`。`enabled`/`adaptive` 会从 `budget_tokens` 或 `output_config.effort` 推导 effort/max_tokens；Claude Code User-Agent 默认 summary `detailed`；`display:"omitted"` 映射为 summary `none`；disabled 映射为 effort `none`。
- `system` 先转 Responses system/developer/instructions 相关 message；`messages[].content[]` 转为 Responses content blocks。
- 支持 text、image/document、tool_use、tool_result、thinking、redacted_thinking、server tool result、MCP/server tool 等，尽量保留为 Responses message/tool/item。
- `tools` 转为 Responses tools；`mcp_servers` 会与 `mcp_toolset` merge，保留 allowed tools 和 Anthropic tool flags。
- `tool_choice` 转为 Responses tool choice。
- route converter 会调用 `normalizeBifrostInputContentBlocks`，避免 string/block 混合导致后续 converter 不稳定。
- 如果 Anthropic request 包含 OpenAI provider + computer/web_search tool，转换时会补 `truncation=auto` 或 `include=["web_search_call.action.sources"]` 这类 OpenAI Responses 语义。

### 4.3 Claude Code / raw passthrough

`checkAnthropicPassthrough` 不是无条件透明代理：

- 必须 `anthropic.IsClaudeCodeRequest(ctx)` 为真，通常由 Claude CLI/Claude Code User-Agent 触发。
- 模型必须是 Anthropic Claude，或 Vertex/Azure 上的 Claude alias。
- 命中后设置 `PassthroughOverridesPresent`、`UseRawRequestBody`、`SendBackRawResponse`。
- 如果不是 API key/OAuth 类请求且 provider 是 Anthropic 或空 provider，会保存完整原始 path、所有 headers，并跳过 key selection。
- API key flow 只透传安全 headers：`anthropic-beta`、`anthropic-dangerous-direct-browser-access`、`anthropic-version`。
- Vertex Claude 如果带 prompt caching scope beta、fast mode beta 或 `output_config.format`，会关闭 raw request body，退回 typed conversion。
- provider request builder 的 raw-body path 仍会处理 model、stream、max_tokens、`fallbacks`、count-tokens 删除字段、tool version remap、`StripAutoInjectableTools`、`StripEmptyThinkingBlocks`、`StripUnsupportedFieldsFromRawBody`、beta header auto-injection、Vertex body/URL 差异。因此 raw path 仍可能删不支持字段、空 thinking block、部分 beta/header 特性。

### 4.4 下游 Anthropic adapter

`ToAnthropicResponsesRequest(ctx, bifrostReq)`：

- `max_output_tokens` -> Anthropic `max_tokens`，缺省使用模型默认上限。
- Opus 4.7+ 会拒绝 `temperature/top_p/top_k`；代码会过滤。普通模型若同时有 `temperature` 和 `top_p`，优先 `temperature`。
- `Params.User` -> `metadata.user_id`。
- system message 或 `Params.Instructions` 提升为 top-level `system`；system message 优先。单条 system-only 输入会改成 user message，以兼容 Anthropic。
- Mid-conversation system 只有 provider/model 支持时才作为 messages 中的 `role:"system"` 发出，否则走 top-level system 或转换。
- Responses messages -> Anthropic `messages[].content[]`。tool result 没有匹配 tool_use 时，不发 `tool_result`，而转成 user text，避免 Anthropic 400。
- reasoning -> `thinking`：`max_tokens=-1` 不支持 Gemini 动态预算，退到最小预算；低于 `MinimumReasoningMaxTokens=1024` 报错；Opus 4.7+ 用 `thinking.type=adaptive`；Opus 4.6+/4.7+ 或 Opus 4.5 可通过 `output_config.effort` 表达 native effort；`summary=none` -> `display=omitted`，否则适用时默认 `summarized`。
- structured output 有两条路径：Vertex 目标使用合成 tool，且 thinking 未开启时强制 `tool_choice`；非 Vertex Anthropic 优先用 GA `output_config.format`。如果输入文件启用了 citations，则不设置 native structured output，避免 Anthropic 不兼容。
- tools 转换支持 function tool、server tools、MCP、Anthropic server tool。会保留版本化 type/name，例如 web_search、web_fetch、computer、text_editor、code_execution、memory、tool_search、mcp_toolset。
- `cache_control` 会保留到 Anthropic 支持的位置，包括 top-level、system/content、tool 层级；tool schema 会 normalize key ordering，减少 prefix cache 因 JSON key 顺序漂移失效。
- provider capability sanitizer 会按 Anthropic/Vertex/Bedrock/Azure 支持度删除字段，例如 unsupported MCP、container、context editing、task_budget、cache_control.scope、tool strict/input_examples、unsupported raw tool type/version 等。

### 4.5 响应、stream、async、错误

- 非流 raw passthrough 条件：没有 structured-output 合成工具名，目标确认为 Claude/Anthropic/Vertex/Azure Claude 路径，并存在 `RawResponse`。
- structured output 合成工具时必须走 typed conversion，避免把内部 `bf_so_*` 工具泄露给上游。
- stream raw passthrough 会跳过 `ContentPartAdded` 的 raw response，因为该 Bifrost 合成事件的 raw 已包含父级 `content_block_start`；直接透传会产生重复 block，让 Anthropic SDK 丢后续 delta。
- typed stream converter 可能把一个 Bifrost chunk 拆成多个 Anthropic SSE event；多事件会拼成多段 `event: ...\ndata: ...\n\n`。
- thinking delta、signature delta、redacted/encrypted thinking 会在 Responses reasoning details 和 Anthropic thinking/redacted_thinking 之间转换，以支持多轮工具调用状态。
- async Messages 可用：pending/processing 返回最小 Anthropic message object，仅含 `id`；completed 后走普通 converter。
- 非流错误转 `ToAnthropicChatCompletionError`；stream 内错误转 `ToAnthropicResponsesStreamError`。

## 5. Gemini / Google GenAI

### 5.1 上游入口

独立 GenAI integration 的 `pathPrefix` 是 `/genai`；下表是 suffix，实际路径会加 `/genai` 前缀。GenAI 核心 route 是动态 path：

| Method | Path suffix | RequestType | Request struct |
|---|---|---|---|
| `POST` | `/v1beta/models/{model:*}` | 由 path suffix、body、large-payload metadata 动态决定 | `GeminiGenerationRequest` 或专用 request |
| `GET` | `/v1beta/models` | `list_models` | `BifrostListModelsRequest` |
| `GET` | `/v1beta/models/{model}` | `list_models`/metadata | `BifrostListModelsRequest` |
| `GET` | `/v1beta/models/{model}/operations/{operation_id:*}` | `video_retrieve` | `BifrostVideoRetrieveRequest` |

`extractModelAndRequestType` / `extractAndSetModelAndRequestType` 从 `{model:*}` 中拆 model 和 action。常见 action：

- `:generateContent` -> `ResponsesRequest`
- `:streamGenerateContent` -> `ResponsesStreamRequest`
- `:countTokens` -> `CountTokensRequest`
- `:embedContent`、`:batchEmbedContents`、非 Imagen `:predict` -> `EmbeddingRequest`
- Imagen `:predict` 或 response modalities 含 `IMAGE` -> image generation/edit
- response modalities 含 `AUDIO` 或 `speechConfig` -> speech
- audio input 且不是 speech -> transcription
- `:predictLongRunning` -> video generation
- `:batchGenerateContent` -> batch create

请求体过大或 request body 是 stream 时，route 不会强制 materialize body 做精确分类，而按 suffix/metadata 保守分类，避免内存峰值。

### 5.2 请求解析和内部格式

`GeminiGenerationRequest.ToBifrostResponsesRequest(ctx)`：

- 默认 provider 是 Gemini，也支持 model provider prefix 和 `x-model-provider`。
- `systemInstruction.parts` 先转 Responses system message。
- `contents[]` 转 `ResponsesMessage`；Gemini role/part 更扁平，part 包括 text、inlineData、fileData、functionCall、functionResponse、thought、thoughtSignature。
- `generationConfig` 转 `ResponsesParameters`：temperature、topP、topK、maxOutputTokens、stopSequences、responseMimeType/responseSchema、thinkingConfig、responseModalities 等按支持度映射或放入 extra。
- `tools[].functionDeclarations` -> Responses function tools。
- `toolConfig.functionCallingConfig` -> Responses tool choice。
- `safetySettings` -> `ExtraParams["safety_settings"]`。
- `cachedContent` -> `ExtraParams["cached_content"]`。
- `serviceTier` -> Bifrost service tier。

GenAI raw body 边界：

- 对 `GeminiGenerationRequest`，只有非 embedding 且 effective provider 是 Gemini 时，route 才保存原始 body 并设置 `UseRawRequestBody`。
- Gemini video/batch create 且 effective provider 是 Gemini 时也保存 raw body。
- provider 侧 `CheckContextAndGetRequestBody` 可直接使用 raw body，但仍会处理 path、model、headers、normalization、large response detection 等外围逻辑。
- generateContent response converter 总是 typed 转回 Gemini shape；部分 file/cached/batch/media endpoint 才有 raw-first。

### 5.3 下游 Gemini adapter

`ToGeminiResponsesRequest(bifrostReq)`：

- model 会 `NormalizeModelName`。
- `ResponsesParameters` -> `generationConfig`、`ExtraParams`、`Tools`、`ToolConfig`、`ServiceTier`。
- `ExtraParams["safety_settings"]` 转回 `SafetySettings` 并从 `ExtraParams` 删除。
- `ExtraParams["cached_content"]` 转回 `CachedContent` 并从 `ExtraParams` 删除。
- Responses system/instructions 转 top-level `systemInstruction`；普通 input/output message 转 `contents[]`。
- function tool 转 `tools[].functionDeclarations`。
- tool choice 转 `toolConfig.functionCallingConfig`：`none -> NONE`，`auto -> AUTO`，`any/required/function -> ANY`，function choice 设置 `AllowedFunctionNames`。旧说法“ANY 会降级 AUTO”已过时，测试覆盖了 ANY 和 allowed names round-trip。
- functionResponse 必须按 Gemini 校验规则独立组织，不能混 text part。Bifrost 会把连续 tool outputs 聚合为一个 Gemini content，且只包含 functionResponse parts。
- function call arguments 会尝试 compact JSON；非法或空字符串会退为 `{}`。

### 5.4 Thinking、thoughtSignature、cache、usage

- Gemini thinking 使用 `generationConfig.thinkingConfig`。
- `effort=none` 设置 `includeThoughts=false` 和 `thinkingBudget=0`。
- 显式 `reasoning.max_tokens` -> `thinkingBudget`，并按模型族范围校验；未知模型跳过范围校验。
- `max_tokens=-1` 表示 Gemini 动态 budget。
- Gemini 3.x 更偏向 `thinkingLevel`；level-only 输入不会反推 budget，round-trip 会再输出 thinkingLevel。
- `thoughtSignature` 是跨轮工具调用关键状态。Gemini 返回时，Bifrost 把它编码进 reasoning encrypted content 或 function call id；后续 functionResponse 再解码回填。缺 signature 时会使用 `skip_thought_signature_validator` sentinel 绕过 Gemini validator。
- usage 会把 `thoughtsTokenCount` 计入 reasoning tokens；把 `cachedContentTokenCount` 和 `cacheTokensDetails` 转为 cached read tokens / modality detail。Gemini response service tier / Vertex traffic type 也映射到 Bifrost service tier。

### 5.5 响应、stream、错误

- GenAI integration 的 generateContent response converter 调 `gemini.ToGeminiResponsesResponse(resp)`。
- stream converter 用 `BifrostToGeminiStreamState` 保存在 context 中，跨 chunk 维护 candidate/content/part、web search grounding、tool call IDs，再输出 Gemini stream event。
- GenAI stream 不发送 OpenAI `[DONE]`。
- Gemini error converter 使用 `gemini.ToGeminiError(err)`。
- provider 支持 large response detection：大响应时把 upstream body reader 放入 context，router 直接流式透传；预读窗口能提取 usage 时会填入轻量 Bifrost response。

## 6. 四类协议的中间格式和丢字段边界

- Chat 内部格式无法完整表达 Responses 的 item reference、MCP approval、code interpreter outputs、namespace tools、复杂 server tool lifecycle；OpenAI Responses fallback 到 Chat 时会降级。
- Anthropic/Gemini server tools 到 OpenAI Responses 可能被过滤、改写或丢弃，取决于 OpenAI tool allowlist。
- OpenAI-only tools 到 Anthropic/Gemini 会按目标 provider 支持度过滤或改写。
- Gemini `safetySettings`、`cachedContent`、`systemInstruction` 是 Gemini 原生概念；跨协议只能放 extra 或转 system/messages，不能保证无损。
- Anthropic thinking signature/encrypted content、Gemini thoughtSignature 只能通过 Responses reasoning details/encrypted content 近似保存；发往不支持 reasoning/encrypted state 的模型时会被剥离。
- `cache_control` 在 Anthropic 语义中可出现在 top-level、system/content/tool；到 OpenAI/Gemini 时只有部分 cache/read/write token 或 provider-specific extra 能表达。
- raw-first 不是跨协议无损方案。只在同协议/同 provider 且启用 raw request/response、passthrough 条件满足时才有意义；即便如此 sanitizer 也可能删除不支持字段。

## 7. 迁移核对清单

- 不要把 Anthropic `/v1/messages` 当内部 Chat；源码走 `BifrostResponsesRequest`。
- 不要漏掉 OpenAI Responses 的 `/openai/responses`、`/openai/responses/input_tokens` suffix 级别名；独立 OpenAI integration 下它们实际是 `/openai/openai/responses`、`/openai/openai/responses/input_tokens`。Azure deployment 动态入口同理实际是 `/openai/openai/deployments/{deploymentPath:*}`。
- 不要把 Anthropic Claude Code passthrough 当逐字节透明代理；User-Agent、模型、auth、Vertex 特例、raw-body sanitizer 都会影响。
- 不要把 Anthropic structured output 一概写成合成工具；当前非 Vertex 优先 `output_config.format`，Vertex 才使用合成 tool 路径。
- 不要把 Gemini `{model:*}` 写成固定 endpoint；request type 由 path suffix、body、large payload metadata 共同决定。
- 不要默认透传 `extra_params`；需要 `x-bf-passthrough-extra-params: true`，large payload 下不抽取。
- 不要统一追加 `[DONE]`；OpenAI Responses、Anthropic Messages、Gemini stream 都不追加。
- 不要承诺跨协议无损；复杂 tools、reasoning encrypted state、thoughtSignature、cache/safety/provider-specific fields 都有过滤、降级或丢字段。
