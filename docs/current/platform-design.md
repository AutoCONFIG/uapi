---
title: UAPI — API 中转平台设计
date: 2026-05-17
status: current-baseline
---

# UAPI — API 中转平台设计文档

> This document is the current platform baseline. For exact implemented routes,
> verification commands, and known gaps, read `docs/current/handoff.md` first.
> For staged scope and no-legacy-burden rules, read `docs/current/roadmap.md`.

## 1. 项目定位

UAPI 是一个**面向公众的高性能 AI API 中转平台**。用户注册账号后购买套餐，获取 API Key 调用 OpenAI/Anthropic/Gemini 等上游服务。管理员管理渠道、上游凭据、套餐、日志和必要控制面。

核心能力：
- 透明代理 + 格式转换（OpenAI Chat Completions API、OpenAI Responses API、Anthropic Messages API、Gemini API 四种格式互转，客户端可用任一原生格式接入）
- 渠道管理（分组、上游凭据、账号元数据、加权轮询、故障冷却、OAuth 自动刷新）
- Code 客户端渠道（Codex、Gemini Code、Claude Code）按本地官方客户端源码对齐；具体源文件见 `docs/current/code-channels.md`
- 双模式计费（次数窗口限额 + Token 额度扣费）
- 用户注册/登录/套餐购买/API Key 管理
- 管理员后台（渠道/账号凭据、节点、用户、套餐、日志、系统设置）
- 本地模型目录（渠道模型配置、手动上游同步、模型重定向；下游模型列表不实时访问上游）
- 结构化运行日志（全局分级 stdout JSON）和可查询调用日志
- 前后端完全分离；Gateway 统一承载控制与调度，Relay 节点只执行转发

## 2. 整体架构

UAPI 当前目标架构是 **单 Gateway 管家 + 一个或多个 Relay 执行节点**。
Gateway 是唯一配置权威，Relay 只负责实际出口转发。完整细节见
`docs/current/gateway-relay.md`。

```
用户浏览器 / 管理员
    │
    ▼
┌─────────────────────────────────────────────┐
│  Frontend + Control API                      │
│    ├── /              → Next.js static UI     │
│    ├── /api/user/*    → 用户控制台 API         │
│    ├── /api/admin/*   → 管理 API              │
│    └── /admin/*       → 管理后台页面           │
└─────────────────┬───────────────────────────┘
                  ▼
            ┌──────────────┐
            │  PostgreSQL  │
            │  users, keys,│
            │  policies,   │
            │  channels,   │
            │  accounts,   │
            │  relay nodes │
            └──────────────┘

下游 API 客户端 (Bearer sk-xxx)
    │
    ▼
┌─────────────────────────────────────────────┐
│  Gateway (/v1/*, /v1beta/*)                  │
│    ├── API Key 鉴权                           │
│    ├── 套餐策略限制（plans.policy_id）          │
│    ├── 计费预检 / 预扣                         │
│    ├── 选择 relay_node + channel + account    │
│    └── HMAC 签名转发给 Relay                   │
└─────────────────┬───────────────────────────┘
                  ▼
┌─────────────────────────────────────────────┐
│  Relay Node(s)                               │
│    ├── 只接受 Gateway 签名请求                 │
│    ├── 执行 Gateway 指定的 channel/account     │
│    ├── provider 格式转换和流式转发              │
│    └── 上报 usage event 给 Gateway             │
└─────────────────────────────────────────────┘
```

关键设计决策：
- **Gateway 是管家和唯一配置权威**：用户、Key、策略、渠道、账号、节点、节点-渠道绑定、计费都由 Gateway/Control Plane 管理。
- **Relay 是执行节点**：不提供管理能力，不做用户 API Key 鉴权，不独立选择账号；只执行 Gateway 指定的 channel/account。节点只绑定渠道，Gateway/Relay 运行时再展开该渠道下的可用账号。
- **Relay 不需要数据库或 Redis**：远端节点只把 Gateway 下发的运行配置放在进程内存，请求热路径不查库、不访问缓存中间件。
- **单机兼容**：没有 active Relay 节点时，Gateway fallback 到本机 relay，适合小规模单机运行。
- **近期扩展目标**：单 Gateway + 2-3 Relay 节点，用于分散出口 IP；暂不引入 CDN、HAProxy、GSLB、多 Gateway 或长连接配置推送。
- **配置同步策略**：Relay 定时拉取 Gateway 下发给自己的运行配置，请求热路径只读本地内存。

## 3. 前端架构

### 技术栈

| 选型 | 版本 | 原因 |
|------|------|------|
| Next.js | 15 (App Router) | React 生态最成熟，SPA 模式运行 |
| Plain CSS | 当前 | 首版控制依赖数量，便于静态导出和快速迭代 |
| lucide-react | 当前 | 图标按钮与后台工具界面 |

设计风格：克制、清晰、偏运营后台。用户控制台保持轻量；管理员控制台只放管理员操作，不混入用户自用入口。

UI 当前阶段先保证功能齐全和操作逻辑清晰，后期可换皮肤不改骨架。

### 页面结构

```
web/
├── app/
│   ├── page.tsx                   # 根页面 (/)
│   ├── layout.tsx                 # 根布局（字体、主题、Provider）
│   ├── globals.css                # 全局样式
│   ├── login/page.tsx             # 登录
│   ├── register/page.tsx          # 注册
│   ├── forgot-password/page.tsx   # 忘记密码
│   ├── overview/page.tsx          # 总览：用量 + 快速开始代码
│   ├── keys/page.tsx              # API 密钥管理
│   ├── usage/page.tsx             # 用量统计（图表 + 明细）
│   ├── plans/page.tsx             # 套餐浏览/购买
│   ├── settings/page.tsx          # 个人设置（密码/邮箱）
│   └── admin/                     # 管理员后台
│       ├── page.tsx               # 管理员入口
│       ├── dashboard/page.tsx
│       ├── relay-nodes/page.tsx
│       ├── channels/page.tsx
│       ├── users/page.tsx
│       ├── plans/page.tsx
│       ├── logs/page.tsx
│       ├── audit-logs/page.tsx
│       ├── settings/page.tsx
│       └── accounts/page.tsx      # 旧链接说明页，账号实际归入渠道
├── components/
│   ├── login-form.tsx             # 登录表单（user + admin 双模式）
│   ├── shell.tsx                  # 导航外壳
│   ├── admin-channel-console.tsx  # 渠道管理控制台
│   └── admin-user-console.tsx     # 用户管理控制台
├── lib/
│   └── api.ts                     # API 客户端（fetch 封装 + JWT 注入）
├── types/
│   └── api.ts                     # TypeScript 类型定义
├── next.config.ts                 # output: "export" + trailingSlash
├── package.json                   # uapi-web, Next 15, React 19
└── tsconfig.json
```

### 用户操作流程

```
注册 → 登录 → 控制台
                ├── 总览：用量概览 + API 快速开始代码块
                ├── API 密钥：默认一个 Key，创建后可再次查看/复制
                ├── 用量：图表 + 请求明细表
                ├── 套餐：当前套餐状态 + 可购套餐 + 兑换码入口
                └── 设置：修改密码 / 邮箱
```

管理员通过 `/admin` 路径访问独立后台（与用户系统隔离）。

## 4. 后端架构

### 分层架构

```
请求 → 路由器 → 中间件链 → Handler → Service → Repository → DB
                                       ↕
                                    缓存/Pool/Adaptor
```

### 路由设计

实际实现的路由（注册于 `internal/server/server.go`）：

```
# ── 用户侧 API（用户 JWT 认证） ──
POST   /api/user/register             # 注册
POST   /api/user/login                # 登录
POST   /api/user/refresh              # 使用 refresh_token 换发 AT/RT

GET    /api/user/profile              # 个人信息
POST   /api/user/password             # 修改密码
POST   /api/user/email                # 修改邮箱

GET    /api/user/keys                 # 我的 API 密钥列表
POST   /api/user/keys                 # 创建 API 密钥
DELETE /api/user/keys/:keyID          # 删除 API 密钥

GET    /api/user/usage                # 用量统计（汇总）
GET    /api/user/usage/logs           # 用量明细（分页）

GET    /api/user/plans                # 套餐列表
GET    /api/user/subscription         # 我的当前套餐
POST   /api/user/subscription/:planID # 购买套餐
POST   /api/user/redeem               # 兑换充值码

# ── 管理员 API（admin JWT 认证） ──
POST   /api/admin/login              # 管理员登录，返回 AT/RT
POST   /api/admin/refresh            # 使用 refresh_token 换发 AT/RT
GET    /api/admin/init-status         # 初始化状态
POST   /api/admin/setup              # 首次初始化，返回 AT/RT

GET    /api/admin/dashboard           # 仪表盘统计
CRUD   /api/admin/access-policies     # 策略资源，由套餐管理页面组合使用；不是独立产品页面
CRUD   /api/admin/relay-nodes
CRUD   /api/admin/node-channels         # 节点-渠道绑定；保留路径名，字段为 channel_id
CRUD   /api/admin/channels            # 渠道管理
POST   /api/admin/channels/models/sync # 管理员手动从上游同步模型到本地渠道配置
POST   /api/admin/channels/oauth/auth-url  # 创建 OAuth 授权 URL（admin JWT）
GET    /api/admin/channels/oauth/callback  # Provider callback（公开回调，state 校验）
POST   /api/admin/channels/oauth/complete  # 手动 callback/token JSON 完成认证
GET    /api/admin/channels/oauth/status    # 查询 OAuth session 状态（admin JWT）
POST   /api/admin/channels/oauth/bind      # 绑定完成的 session 为 oauth_token account
CRUD   /api/admin/accounts            # 账号管理；凭据导出必须二次校验管理员密码
CRUD   /api/admin/tokens              # 内部 API；管理界面不提供管理员生成/使用用户 Key 的入口
CRUD   /api/admin/plans               # 套餐管理
CRUD   /api/admin/redeem-codes        # 套餐兑换码
GET/PUT /api/admin/settings           # 系统设置，例如日志/兑换码保留时间
GET    /api/admin/users               # 用户列表
PUT    /api/admin/users               # 用户管理
DELETE /api/admin/users               # 用户管理
GET    /api/admin/logs                # 调用日志
GET    /api/admin/audit-logs          # 系统审计

# ── Gateway / Relay API（Gateway 鉴权调度，Relay 执行） ──
ANY    /v1/chat/completions           # OpenAI Chat Completions API 格式
ANY    /v1/responses                  # OpenAI Responses API 格式
GET    /v1/models                     # OpenAI/Anthropic 兼容模型列表；只读本地模型目录
ANY    /v1/images/*                   # OpenAI 兼容图片端点，需上游渠道支持
ANY    /v1/messages                   # Anthropic Messages API 格式
GET    /v1beta/models                 # Gemini 兼容模型列表；只读本地模型目录
ANY    /v1beta/*                      # Gemini generateContent API 格式
```

### 渠道和 Code 客户端

渠道用 `type` 表示供应商家族，用 `api_format` 表示具体协议变体。
OpenAI 支持 `standard` Chat Completions、`responses` 和 `codex`；Gemini 支持
`standard` 和 `gemini_code`；Anthropic 支持 `standard` 和
`claude_code`。渠道只表达供应商、协议和模型范围；上游接入点归属账号。
API Key 账号可单独配置接入点；Web UI 拆成 Base URL 和路径前缀两个输入，路径留空时使用 `/v1` 或 `/v1beta` 等标准路由，保存到后端时仍合成为完整上游 URL。OAuth/Code 账号在绑定时由后端写入供应商默认接入点，
Web UI 不提供手工编辑入口。Web UI 直接按渠道展示，账号在渠道内以卡片管理；
`channel_group` 不再作为用户可见的一级分组。

模型列表是本地控制面数据。`channels.models` 保存规范化后的上游模型 ID，`channels.model_aliases` 保存“上游模型=对外模型”的映射。下游 `/v1/models` 和 `/v1beta/models` 只读取本地数据库并结合用户套餐过滤，不在请求时访问上游。管理员需要刷新模型时，通过渠道页面调用 `POST /api/admin/channels/models/sync?id=<channel_id>` 手动同步。

Code 客户端行为不从公开 API 猜测，必须从本地 upstream 官方客户端源码对齐：

- Codex: `upstream/codex/codex-rs/login/src/*`
- Gemini Code: `upstream/gemini-cli/packages/core/src/code_assist/*`
- Claude Code: `upstream/claude-code/src/services/oauth/*`,
  `upstream/claude-code/src/services/api/client.ts`,
  `upstream/claude-code/src/utils/http.ts`

完整对齐清单见 `docs/current/code-channels.md`。

### 中间件链

```
用户/管理 API：  CORS → Access Token JWT 认证 → Handler
Gateway API：    API Key 认证 → 套餐策略 → 并发检查 → 计费预检 → 调度 → Relay
Relay API：      Gateway HMAC 签名校验 → 执行指定 channel/account → provider 转发
```

`/v1/*` 和 `/v1beta/*` 当前先进入 Gateway。Relay 节点在生产远端模式下应开启 `gateway.require_internal`，只接受 Gateway 签名请求。

用户和管理员登录统一使用 AT/RT：`auth.access_token_expiry` 默认 `15m`，`auth.refresh_token_expiry` 默认 `720h`。Access Token 只用于业务接口鉴权，Refresh Token 只用于 `/api/user/refresh` 和 `/api/admin/refresh` 换发新 token pair。Refresh Token 绑定当前密码哈希指纹，密码变更后旧 RT 自动失效。当前实现不维护服务端 session 表，以保持低资源占用；需要踢下线、设备管理或两步验证强绑定时再引入 refresh token 版本号或会话表。

### 多格式中转

客户端可用四种原生格式中的任一种接入 Gateway。Gateway 完成鉴权、策略限制和调度后，Relay 根据入口路径判断客户端格式，再按 Gateway 指定的上游渠道/账号进行格式转换：

```
入口路径                 客户端格式
/v1/chat/completions    OpenAI Chat Completions API
/v1/responses           OpenAI Responses API
/v1/images/*            OpenAI Images API
/v1/messages            Anthropic Messages API
/v1beta/*               Gemini generateContent API
```

#### 转换策略：跨协议中间格式，同协议 raw preservation

跨协议采用统一中间格式（`InternalRequest`/`InternalResponse`）：

```
客户端格式 → ToInternal() → InternalRequest → FromInternal() → 上游格式
```

同协议请求/响应不强制进入这个较窄的中间结构，而是 raw preservation：
下游协议和上游协议相同时保留原始 JSON/SSE，只补必要的路由、鉴权和计费
上下文。这样不会因为中间格式暂时没有表达某个原生标准字段而丢精度。新增
provider 仍按 Bifrost-style adaptor pipeline 接入；跨协议转换时才使用
`ToInternal()`/`FromInternal()`。跨协议转换采用粗转换策略：能映射的
等价字段必须映射；目标协议无法表达但不影响核心 prompt/tool 流程的字段
记录 warning 后跳过，并保留在同协议 raw preservation/ExtraParams 路径中；
只有畸形结构、必需字段缺失或会让目标请求语义无效的内容才返回显式转换错误。

流式 SSE 不整流缓冲，采用事件级状态机转换。和 Bifrost 的 schema/adaptor
思路一致，Relay 先把不同上游流规范化为内部 OpenAI Chat Completions-style chunk，再按
下游协议输出 OpenAI Responses API、Gemini API、Anthropic Messages API、OpenAI Chat Completions API 对应的 SSE 事件：

```
上游 SSE event → provider stream normalizer → client stream formatter → 客户端 SSE event
```

`force_stream` 会把上游 SSE 规范化为 OpenAI Chat Completions-style chunk 后汇总成非流
JSON。若上游流中出现 error chunk，Relay 返回对应客户端协议的错误响应，
不生成空 completion。

#### 协议规范化

Relay 的跨协议请求不依赖上游原始 envelope 透传，而是经过 UAPI 的
ToInternal/FromInternal 或流式 normalizer/formatter。同名标准协议是一个
例外：如果下游和上游都是同一个标准协议，Relay 保留原始标准 JSON 或 SSE，
避免丢失 Gemini API、Anthropic Messages API、OpenAI 原生扩展字段。所有格式都必须实现
`ParseUsage` 和 `ParseStreamUsage`，Relay 需要从响应中提取 usage；目标架构中
usage event 回报 Gateway 后由 Gateway 幂等结算。

Token 统一计量：无论客户端用哪种格式，usage 都归一化为 `prompt_tokens + completion_tokens` 进行计费。

## 5. 数据模型

### User 模型 (`internal/db/user.go`)

```go
type User struct {
    Base
    Email        string `gorm:"size:255;uniqueIndex;not null"`
    Username     string `gorm:"size:100;uniqueIndex;not null"`
    PasswordHash string `gorm:"size:255;not null"`
    Status       string `gorm:"size:20;default:active"`  // active, disabled
    Balance      int64  `gorm:"default:0"`               // 余额（token 单位）
}
```

### Account 模型 (`internal/db/account.go`)

```go
type Account struct {
    Base
    ChannelID     uuid.UUID  `gorm:"type:uuid;index;not null"`
    Name          string     `gorm:"size:100;not null"`
    Credentials   string     `gorm:"type:text;not null"`        // AES-256-GCM encrypted
    CredType      string     `gorm:"size:20;default:api_key"`   // api_key | oauth_token
    Endpoint      string     `gorm:"size:500"`                  // upstream URL prefix for this account
    Weight        int        `gorm:"default:1"`
    Enabled       bool       `gorm:"default:true"`
    CooldownUntil *time.Time
    RefreshToken  string     `gorm:"type:text"`                 // AES encrypted (for oauth_token)
    TokenExpiry   *time.Time                                    // access_token expiry
    ClientID      string     `gorm:"type:text"`                 // OAuth client ID
    ClientSecret  string     `gorm:"type:text"`                 // AES encrypted OAuth client secret
    TokenURL      string     `gorm:"type:text"`                 // OAuth token endpoint
}
```

### Token 模型 (`internal/db/token.go`)

```go
type Token struct {
    Base
    UserID      string     `gorm:"size:36;index"`           // 关联 User
    Name        string     `gorm:"size:100;not null"`
    Key         string     `gorm:"size:100;uniqueIndex;not null"`
    Enabled     bool       `gorm:"default:true"`
    IPWhitelist string     `gorm:"type:text"`
    ExpiresAt   *time.Time
    Models      string     `gorm:"type:text"`
    Permissions string     `gorm:"type:text"`
    Unlimited   bool       `gorm:"default:false"`
}
```

### RedeemCode 模型 (`internal/db/redeem_code.go`)

```go
type RedeemCode struct {
    Base
    Code      string     `gorm:"size:100;uniqueIndex;not null"`
    Value     int64      `gorm:"not null"`
    UsedBy    *string    `gorm:"size:36;index"`
    UsedAt    *time.Time
    Status    string     `gorm:"size:20;default:active"`  // active, used, expired
    ExpiresAt time.Time  `gorm:"not null"`
}
```

其他模型（Channel, Plan, TokenPlan, Log, AuditLog）参见 `internal/db/` 对应文件。

### Usage 响应类型 (`internal/user/dto.go`)

`GET /api/user/usage` 返回 `UsageSummaryResponse`，包含总请求、失败请求、
成功率、prompt/completion/total token、按模型聚合和最近 7 天趋势。

`GET /api/user/usage/logs` 返回 `UsageLogsResponse`，包含分页字段和
`UsageLogItem[]`，每条日志都有模型、stream 标记、token 明细、延迟、状态码和错误信息。

## 6. 日志系统

后端使用 `internal/logger` 作为全局结构化日志入口。日志写到 stdout，
每行是 JSON，包含 `ts`、`level`、`component`、`msg` 以及调用方传入的字段。
日志级别来自 `config.yaml` 的 `logging.level`，支持 `debug`、`info`、
`warn`/`warning`、`error`，默认 `info`。

开发阶段可以把 `logging.level` 设置为 `debug`。上线时建议调回 `info`
或 `warn`，因为 debug 会记录每个 HTTP 请求和 relay 路由决策。日志字段
不得包含 API Key、Authorization header、OAuth access token、refresh token、
id_token 或完整请求/响应正文；需要排查上游错误时只记录截断后的错误体和
channel/account/model/project 等非密钥上下文。
`internal/logger` 对常见凭据字段和字符串中的 Bearer/API key/OAuth token
形态做兜底 redaction；调用方仍应避免主动记录完整请求体和上游 credential。

组件命名用于排查链路，例如：

- `app`：进程启动、数据库连接、服务退出。
- `server`：HTTP 服务监听。
- `server.request`：debug 级 HTTP 请求完成日志，包含 method/path/status/latency/body size/remote IP。
- `relay.upstream` / `relay.stream` / `relay.convert`：转发、流式处理、格式转换。
- `relay.route`：debug 级路由决策日志，包含 token id、channel/account、client/upstream format、stream/force-stream 等。
- `relay.gemini_code`：Gemini Code 上游错误诊断，包含 channel/account、上游模型、project、credit 类型和截断后的错误体。
- `relay.ws`：Responses WebSocket 代理和 SSE bridge。
- `gateway`、`admin.scheduler`、`relay.billing`、`relay.credentials`：对应子系统。

请求级日志仍写入数据库 `logs` 表，并通过后台 `/api/admin/logs` 查询。
上游失败会把解析后的错误信息写入 `logs.error_message`，用于在后台直接定位
429、认证失败、配额耗尽等问题。stdout 结构化日志保留更详细的上下文字段，
数据库日志保留适合列表查询的摘要。

## 7. OAuth 凭证自动刷新

参考 upstream 中 Codex CLI、Gemini CLI 和 Claude Code 的 OAuth 实现，relay 支持用 OAuth token 而非静态 API Key 作为上游凭证。

管理端 OAuth onboarding 已在 `internal/admin/oauth_handler.go` 实现：

1. `POST /api/admin/channels/oauth/auth-url` 创建短期内存 session、PKCE verifier/challenge 和 provider 授权 URL。
2. `POST /api/admin/channels/oauth/complete` 接收官方客户端回调 URL 或 token JSON，交换/导入凭证并保留在 session 中。`GET /api/admin/channels/oauth/callback` 仍保留给 UAPI-hosted callback 流程。
3. `GET /api/admin/channels/oauth/status` 供前端轮询 callback 状态。
4. `POST /api/admin/channels/oauth/bind` 由已登录管理员把完成的 session 保存为 `accounts.cred_type = oauth_token`。

Code 渠道认证和刷新细节以 `docs/current/code-channels.md` 为准。当前规则包括：Codex 在 token 过期或上次刷新超过 8 天时刷新；Gemini Code 和 Claude Code 在距过期 5 分钟内刷新，并由 Gateway 侧 idle maintenance 做保底维护。

### Relay 中的刷新逻辑

实际实现在 `internal/relay/account_refresh.go`，签名为：

```go
// EnsureValidCredentials 检查账号凭证是否有效，必要时刷新
func EnsureValidCredentials(account *db.Account, database *gorm.DB) (string, error) {
    if account.CredType == "api_key" || account.CredType == "" {
        return crypto.Decrypt(account.Credentials)
    }
    // OAuth token — 按 provider 生命周期检查，使用 singleflight 去重并发刷新
    // Codex: token 过期或上次刷新超过 8 天
    // Gemini Code/Claude Code: 距过期 5 分钟内
    if shouldRefreshOAuthCredentials(account) {
        v, err, _ := refreshGroup.Do(account.ID.String(), func() (interface{}, error) {
            return refreshOAuthToken(account, database)
        })
        if err != nil {
            return "", err  // 刷新失败 → 账号 cooldown，换下一个
        }
        return v.(string), nil
    }
    return crypto.Decrypt(account.Credentials)
}
```

`refreshOAuthToken` 成功后在 Gateway/本机模式下同步更新数据库。远端 `server.mode: relay`
不直连数据库，刷新结果会先更新进程内 runtime account，再通过
`POST /internal/relay/account-update` 回写 Gateway。该回写当前是 best-effort：
Gateway 不可达时 Relay 会保留内存中的新凭证并记录 warning，但不会持久重试。
使用 `singleflight.Group` 防止同一账号的并发刷新。

## 7. 计费系统

保持现有 PreConsume → Settle + Refund 模式，新增用户维度：

- **Token 绑定用户**：每个 API Key 关联一个 User
- **用户余额**：当前运行时在没有 active token plan 时只检查用户余额为正；
  token_based 套餐实际扣减 `token_plans.used_quota`，不从 `users.balance`
  扣费。后续接入支付/余额结算时需要再补用户余额扣减策略。
- **充值码兑换**：充值码增加用户余额（支付接口后期接入）
- **管理员调整**：管理员可直接调整用户余额

## 8. 部署架构

```
单机部署（当前）：
Docker Compose:
  ├── postgres
  ├── uapi (server.mode=all, Gateway + 本机 Relay)
  └── web (Nginx 静态前端 + /api、/v1 和 /v1beta 反代)

只有 web 暴露宿主机端口 80。PostgreSQL、Gateway 8080、本机 Relay 都只在 Docker 内部网络可见。

Nginx / web container (80)
  ├── / → Next.js 静态文件 (/opt/uapi/web/out)
  ├── /api/user/* → Go API Server (compose: http://uapi:8080)
  ├── /api/admin/* → Go API Server (compose: http://uapi:8080)
  ├── /v1/chat/completions → Go API Server (compose: http://uapi:8080)
  ├── /v1/responses → Go API Server (compose: http://uapi:8080)
  ├── /v1/messages → Go API Server (compose: http://uapi:8080)
  ├── /v1/images/* → Go API Server (compose: http://uapi:8080)
  └── /v1beta/* → Go API Server (compose: http://uapi:8080)

Go Server (单进程，fasthttp)
PostgreSQL (本地或远程)

多 Relay 节点部署：
同机分离测试使用 `docker-compose.gateway.yaml` + `docker-compose.relay.yaml`，二者共享 `uapi-net`，Relay 不暴露宿主机端口，后台节点 `base_url` 填 `http://relay:8081`。

远端 Relay 机器使用 `docker-compose.relay.remote.yaml` + `config.relay.yaml`：
  - `server.mode: relay`
  - `gateway.require_internal: true`
  - `gateway.control_url` 指向 Gateway 可访问地址
  - `gateway.relay_node_id` 填管理后台创建的节点 ID
Relay 节点不启动 PostgreSQL/Redis，只保留内存运行时配置和短期状态。
```

前端构建：`next build` 静态导出 → Nginx 托管。
后端构建：`go build` → 单二进制；推荐 Docker Compose 管理。

详见 `docs/deployment/nginx.md`。

## 9. 不在范围内

- 在线支付接入（支付宝/微信/Stripe）— 后期加
- 邮箱验证 — 后期加
- 多租户/组织 — 后期加
- 多 Gateway / 分布式控制面 — 当前单 Gateway 足够
- WebSocket Realtime API 完整双向会话 — 当前无需求；all-in-one 模式已接入
  `/v1/responses` Responses WebSocket 的 `response.create` turn。分离式
  Gateway/Relay 部署当前仍使用 HTTP/SSE 路径，跨 Gateway 的 WS relay 后续再做。
