# UAPI 当前交接

本文是新会话进入项目时的第一份文档。它只记录当前仓库的实现状态，不记录历史分支流水账。

## 项目定位

UAPI 是统一 AI API Gateway。它提供用户注册、API Key、套餐额度、渠道和上游账号管理，并把下游 OpenAI/Gemini/Anthropic 兼容请求调度到本地或远程 Relay 执行。

## 技术栈

- 后端：Go、fasthttp、GORM、PostgreSQL、JWT、AES-256-GCM。
- 前端：Next.js 15 App Router 静态导出、React 19、TypeScript、纯 CSS、lucide-react。
- 入口：`cmd/uapi/main.go`。
- 配置：`config.example.yaml`、`config.gateway.example.yaml`、`config.relay.example.yaml`。

## 运行模式

`server.mode` 支持三种模式：

- `all`：默认模式，Gateway、控制 API 和本地 Relay 在同一进程内运行。
- `gateway`：只运行控制面和 Gateway，调度远程 Relay。
- `relay`：只运行执行节点，不连接 PostgreSQL；必须设置 `gateway.require_internal: true`、`gateway.control_url` 和 `gateway.relay_node_id`。

## 当前文档

- `docs/current/platform-design.md`：平台和后端实现事实。
- `docs/current/gateway-relay.md`：Gateway/Relay 架构事实。
- `docs/current/frontend.md`：前端事实。
- `docs/current/oauth-channels.md`：OAuth 渠道事实。
- `docs/current/roadmap.md`：当前范围和暂缓项。
- `docs/deployment/nginx.md`：部署说明。

## 主要路由

公共和内部路由在 `internal/server/server.go` 注册。`/v1/` 与 `/v1beta/` 不走普通 Router，而是直接进入 Gateway 或 Relay。

### 公共设置

```text
GET  /healthz
GET  /api/public/settings
GET  /api/public/wallpaper
```

### 用户 API

```text
POST   /api/user/register
POST   /api/user/login
POST   /api/user/refresh
GET    /api/user/profile
POST   /api/user/password
POST   /api/user/email
GET    /api/user/keys
POST   /api/user/keys
DELETE /api/user/keys/:keyID
GET    /api/user/usage
GET    /api/user/usage/logs
GET    /api/user/subscription
GET    /api/user/plans
GET    /api/user/models
POST   /api/user/redeem
```

### 管理 API

```text
POST   /api/admin/login
POST   /api/admin/refresh
GET    /api/admin/init-status
POST   /api/admin/setup
GET    /api/admin/dashboard
CRUD   /api/admin/access-policies
CRUD   /api/admin/relay-nodes
CRUD   /api/admin/node-channels
GET    /api/admin/channels/catalog
POST   /api/admin/channels/models/sync
CRUD   /api/admin/channels
POST   /api/admin/accounts/export
POST   /api/admin/accounts/:id/refresh-quota
POST   /api/admin/channels/:id/refresh-quota
CRUD   /api/admin/accounts
CRUD   /api/admin/tokens
CRUD   /api/admin/plans
GET    /api/admin/logs
GET    /api/admin/audit-logs
GET/PUT /api/admin/settings
POST   /api/admin/settings/export
POST   /api/admin/settings/import
POST   /api/admin/settings/wallpaper
GET/POST/DELETE /api/admin/redeem-codes
GET/PUT/DELETE /api/admin/users
POST   /api/admin/users/export
POST   /api/admin/users/import
```

### OAuth 管理

```text
POST /api/admin/channels/oauth/auth-url
GET  /api/admin/channels/oauth/callback
POST /api/admin/channels/oauth/complete
GET  /api/admin/channels/oauth/status
POST /api/admin/channels/oauth/bind
```

### Relay 内部 API

```text
GET  /internal/relay/config
POST /internal/relay/usage-events
POST /internal/relay/account-update
```

这些内部路由使用 `X-UAPI-Internal-Secret` 校验。

### 下游模型 API

```text
ANY /v1/chat/completions
ANY /v1/responses
GET /v1/models
ANY /v1/images/*
ANY /v1/audio/*
ANY /v1/embeddings
ANY /v1/moderations
ANY /v1/realtime/*
ANY /v1/videos*
ANY /v1/video/*
ANY /v1/messages
GET /v1beta/models
ANY /v1beta/*
```

`/v1/models` 和 `/v1beta/models` 只读本地数据库，不在用户请求时访问上游。

## 前端路由

```text
/
/login
/register
/forgot-password
/setup
/overview
/keys
/usage
/plans
/settings
/admin
/admin/dashboard
/admin/relay-nodes
/admin/channels
/admin/users
/admin/plans
/admin/logs
/admin/audit-logs
/admin/settings
```

## 常用命令

```bash
go test ./...
go build ./...
go run ./cmd/uapi/

npm --prefix web install
npm --prefix web run build
npm --prefix web run serve:static
```

## 提交前检查

```bash
go test ./...
npm --prefix web run build
git diff --check
```

## 当前已知边界

- 远程 Relay 的 OAuth 账号刷新结果会 best-effort 回写 Gateway；没有持久重试队列。
- 远程 Relay 的 usage event 回传没有持久重试队列。
- Relay 运行时凭据仍使用共享加密密钥，未实现节点级凭据加密。
- 分离式 Gateway/Relay 部署当前走 HTTP/SSE Relay 路径；`/v1/responses` WebSocket 仅在 `all` 模式内由本地 WS handler 处理。
