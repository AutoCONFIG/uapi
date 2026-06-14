# UAPI 平台设计

本文按当前代码整理。实现细节以 `internal/`、`web/` 和配置示例为准。

## 核心职责

UAPI 由控制面、Gateway、Relay 和前端组成：

- 控制面管理用户、管理员、API Key、套餐、访问策略、渠道、账号、Relay 节点、日志和系统设置。
- Gateway 处理下游 `/v1/*`、`/v1beta/*` 请求，完成 API Key 鉴权、套餐策略、模型可见性、并发限制、预扣费、节点/渠道/账号调度和 HMAC 转发。
- Relay 执行上游请求，负责凭据刷新、协议转换、流式转发、用量解析和 usage event 上报。
- 前端是静态导出的管理界面，不保存业务状态。

## 代码结构

```text
cmd/uapi-gateway/         Gateway + embedded Web 程序入口
cmd/uapi-relay/           Relay 程序入口
internal/server/          Gateway fasthttp 服务和路由
internal/relayserver/     Relay 内部 HTTP 服务
internal/gateway/         Gateway 鉴权、策略、模型列表、调度、反向代理
internal/relay/           Relay 执行、上游计量解析、并发、流式、provider-native WS/realtime、运行时配置
internal/relay/provider/  上游适配器、协议 schema、IR 转换
internal/admin/           管理 API、OAuth、导入导出、调度器
internal/user/            用户 API
internal/auth/            JWT 与用户中间件
internal/db/              GORM 模型和 AutoMigrate
internal/quota/           OAuth provider quota/usage 同步
internal/oauthprovider/   OAuth provider 注册表
internal/config/          配置加载、默认值、校验
web/                      Next.js 静态前端
docs/                     项目文档和外部 API reference
```

## 配置事实

`config.Load` 会在配置文件不存在或 secret 缺失时自动生成强 secret 并写回文件。关键配置：

- `server.max_body_size_mb`: 默认 256。
- `server.stream_idle_timeout_seconds`: 默认 1800。
- `security.jwt_secret`: JWT secret，至少 32 字符。
- `security.encryption_key`: 32 字节 hex，用于 AES-256-GCM。
- `security.trusted_proxies`: 允许信任 `X-Forwarded-For`/`X-Real-IP` 的代理。
- `gateway.internal_secret`: Gateway/Relay 内部认证 secret。
- `gateway.config_pull_interval`: Relay 拉取运行时配置间隔，默认 5 秒。
- `ws.max_message_size_mb`: 默认 256。
- `logging.level`: 默认 `info`。

`uapi-relay` 启动时必须设置 `gateway.require_internal: true`、`gateway.control_url` 和非占位 UUID 的 `gateway.relay_node_id`。

## 数据模型

AutoMigrate 模型在 `internal/db/db.go` 的 `AllModels` 中维护：

```text
Channel, Account, Token, Plan, TokenPlan, Log, AuditLog,
User, RedeemCode, RelayNode, NodeChannel, AccessPolicy,
PolicyUsageWindow, UsageEvent, SystemSetting
```

关键业务含义：

- `channels.type` 表示 provider 家族，如 `openai`、`gemini`、`anthropic`、`antigravity`。
- `channels.api_format` 表示协议/客户端变体，如 `standard`、`responses`、`codex`、`gemini_code`、`claude_code`、`antigravity`。
- `channels.models` 是本地模型目录。
- `channels.model_aliases` 使用 `upstream=public` 每行一条，控制对外模型名。
- `accounts.cred_type` 为 `api_key` 或 `oauth_token`。
- `accounts.credentials`、`accounts.refresh_token`、`accounts.client_secret` 加密保存。
- `accounts.metadata` 保存 OAuth provider 的账号、项目、额度等展示字段。
- `token_plans` 当前表示用户套餐订阅，不表示 API Key 与套餐的绑定。
- API Key 的 `models`、`permissions`、`ip_whitelist`、`expires_at` 是 key 级限制；套餐和策略来自 key 所属用户的 active plan。

## 鉴权

用户和管理员使用独立 JWT 流程：

- 用户：`/api/user/login`、`/api/user/refresh`。
- 管理员：`/api/admin/login`、`/api/admin/refresh`。
- Access token 默认 15 分钟，refresh token 默认 720 小时。

下游模型 API 使用 API Key：

- `Authorization: Bearer <key>`。
- 模型列表也接受 Anthropic SDK 常用的 `x-api-key`，但不能同时依赖匿名访问。
- Gateway 会校验 key 是否启用、是否过期、IP 白名单、模型限制和 endpoint permission。

Gateway 到 Relay 的 `/v1/*`、`/v1beta/*` 等数据面请求使用 HMAC 签名；Relay 到 Gateway 的 `/internal/config`、`/internal/usage`、`/internal/account`、`/internal/dumps` 使用 `X-UAPI-Internal-Secret`。

## 套餐、策略和计费

调用模型 API 必须有 active user subscription。Gateway 通过：

```text
tokens.user_id -> token_plans.user_id -> plans.policy_id -> access_policies
```

加载当前策略。

策略当前包含：

- allowed models
- hourly/weekly/monthly usage windows
- max concurrency

`count_based` 套餐按请求数计量，`token_based` 套餐按 token 估算预扣并在响应结束后结算。配置值 `0` 表示没有可用额度，不表示无限。

计费流程：

1. Gateway 做订阅和策略检查。
2. Gateway 预扣估算额度并生成 `request_id`。
3. Relay 执行请求并解析 usage。
4. Relay 上报 usage event。
5. Gateway 按 `request_id` 幂等结算或退款。

## 模型列表和模型映射

`GET /v1/models` 和 `GET /v1beta/models` 由 Gateway 本地处理。可见模型来自：

- enabled channel 的 `models` 和 `model_aliases`。
- enabled account 存在。
- active plan policy 或 API Key 自身 `models` 限制。

用户请求模型列表时不访问上游。管理员需要刷新某个渠道的本地模型目录时使用：

```text
POST /api/admin/channels/models/sync?id=<channel_id>
```

## 协议转换

下游协议由请求路径决定，上游协议由选中的 channel `type + api_format` 决定。模型名只参与路由和模型映射，不决定协议。

文本类协议：

- OpenAI Chat Completions：`/v1/chat/completions`
- OpenAI Responses：`/v1/responses`
- Anthropic Messages：`/v1/messages`
- Gemini generateContent：`/v1beta/*`

转换策略：

- 同协议请求尽量保留标准原始 body/stream，仅做已知客户端噪声清理和 parser 校验。
- 跨协议请求进入 `ir.Request`，响应进入 `ir.Response`，流式响应按事件级 IR 转换。
- 能等价表达的字段必须映射；不能表达且不影响核心 prompt/tool 流程的字段记录 warning 后跳过。
- 结构畸形、必需字段缺失、或会导致目标协议语义无效时返回显式转换错误。

非文本能力：

- OpenAI-compatible Images/Audio/Embeddings/Moderations/Realtime HTTP/Video 只在明确支持的 provider 上处理。
- Images generation/edit/variation 可转换到 Antigravity `requestType: "image_gen"`。
- Audio、Embeddings、Moderations、Realtime、Video 目前只透传到 OpenAI-compatible 上游；其他 provider 返回 unsupported。
- WebSocket 不作为普通 `/v1/*` Gateway -> Relay 内部传输。严格 split 部署下，Relay 不能把历史 WS 入口直接暴露给用户；后续只有在 Gateway 完成鉴权、策略、计费、路由和 HMAC 转发后，才允许作为 provider-native realtime/Codex 类专用能力启用。

流式处理：

- SSE 不整体缓冲。
- Same-protocol SSE 保留 event 名和有效 data 空白。
- 仅允许去掉 `data:` 后的一个可选空格。
- `server.stream_idle_timeout_seconds` 控制上游流长时间无 chunk 时的应用层 idle timeout。

## 日志

后端使用 `internal/logger` 输出结构化 JSON 到 stdout。数据库请求日志写入 `logs` 表并通过后台查询。

日志约束：

- 不记录完整 Authorization、API Key、OAuth token、refresh token、id token、完整 credential 或完整请求体。
- 全局 logger 有 credential 字段和 token 形态的兜底 redaction，但 handler 仍应主动避免输出敏感数据。
- 常见组件名包括 `server`、`server.request`、`gateway`、`gateway.models`、`gateway.relay`、`relay.upstream`、`relay.stream`、`relay.convert`、`relay.ws`、`admin.oauth`、`admin.scheduler`。
