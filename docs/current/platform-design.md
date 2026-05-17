---
title: UAPI — API 中转平台设计
date: 2026-05-17
status: current-baseline
---

# UAPI — API 中转平台设计文档

> This document is the current platform baseline. For exact implemented routes,
> verification commands, and known gaps, read `docs/current/handoff.md` first.

## 1. 项目定位

UAPI 是一个**面向公众的 AI API 中转平台**。用户注册账号后购买套餐，获取 API Key 调用 OpenAI/Anthropic/Gemini 等上游服务。管理员管理渠道、上游凭据、计费等后台功能。

核心能力：
- 透明代理 + 格式转换（OpenAI Chat/Responses、Anthropic、Gemini 四种格式互转，客户端可用任一原生格式接入）
- 渠道管理（上游凭据 + 加权轮询 + 故障冷却 + OAuth 自动刷新）
- 双模式计费（次数窗口限额 + Token 额度扣费）
- 用户注册/登录/套餐购买/API Key 管理
- 管理员后台（渠道/用户/令牌/计费管理）
- 前后端完全分离，后端极致性能不受前端影响

## 2. 整体架构

```
用户浏览器                     下游客户端 (OpenAI 兼容格式, Bearer sk-xxx)
    │                              │
    │  HTTPS                       │  HTTPS
    ▼                              ▼
┌─────────────────────────────────────────────┐
│  nginx                                       │
│    ├── /           → 前端静态文件 (Next.js)   │
│    ├── /api/user/* → 后端用户 API             │
│    ├── /api/admin/*→ 后端管理 API             │
│    ├── /v1/*       → 后端中转 API (OpenAI格式)     │
│    ├── /v1/messages→ 后端中转 API (Anthropic格式)   │
│    └── /v1beta/*   → 后端中转 API (Gemini格式)      │
└─────────────────────────────────────────────┘
         │                    │                │
         ▼                    ▼                ▼
┌──────────────┐  ┌───────────────┐  ┌──────────────────┐
│  Next.js SPA │  │ Go API Server │  │ Go Relay Engine  │
│  (Nginx托管)  │  │ (用户/管理API) │  │ (fasthttp, 性能关键)│
└──────────────┘  └───────┬───────┘  └────────┬─────────┘
                          │                    │
                          ▼                    ▼
                    ┌──────────────────────────────┐
                    │  PostgreSQL                   │
                    │  users, tokens, channels,     │
                    │  accounts, plans, logs        │
                    └──────────────────────────────┘
```

关键设计决策：
- **前端由 Nginx 托管静态文件**，后端 Go 服务完全不服务前端 HTML，零性能影响
- **中转路径 `/v1/*` 走独立的 fasthttp handler**，不经过任何用户系统的中间件开销
- **前后端通过 REST API 通信**，前端 Next.js SPA 调后端 JSON 接口

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
│       ├── channels/page.tsx
│       ├── users/page.tsx
│       ├── tokens/page.tsx
│       ├── plans/page.tsx
│       ├── logs/page.tsx
│       ├── audit-logs/page.tsx
│       └── accounts/page.tsx      # 兼容页
├── components/
│   ├── login-form.tsx             # 登录表单（user + admin 双模式）
│   ├── shell.tsx                  # 导航外壳
│   ├── admin-channel-console.tsx  # 渠道管理控制台
│   └── admin-user-console.tsx     # 用户管理控制台
├── lib/
│   ├── api.ts                     # API 客户端（fetch 封装 + JWT 注入）
│   └── mock.ts                    # 静态预览 mock 数据
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
                ├── API 密钥：创建/删除/复制，一目了然
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
POST   /api/user/refresh              # 刷新 JWT

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
POST   /api/admin/login              # 管理员登录
GET    /api/admin/init-status         # 初始化状态
POST   /api/admin/setup              # 首次初始化

GET    /api/admin/dashboard           # 仪表盘统计
CRUD   /api/admin/channels            # 渠道管理
CRUD   /api/admin/accounts            # 兼容接口；前端已归一到渠道
CRUD   /api/admin/tokens              # 令牌管理
CRUD   /api/admin/plans               # 套餐管理
GET    /api/admin/users               # 用户列表
PUT    /api/admin/users               # 用户管理
DELETE /api/admin/users               # 用户管理
GET    /api/admin/logs                # 请求日志
GET    /api/admin/audit-logs          # 审计日志

# ── 中转 API（API Key 认证，极致性能路径） ──
ANY    /v1/chat/completions           # OpenAI Chat Completions 格式
ANY    /v1/responses                  # OpenAI Responses API 格式
ANY    /v1/messages                   # Anthropic Messages 格式
ANY    /v1beta/*                      # Gemini generateContent 格式
```

### 中间件链

```
用户/管理 API：  CORS → JWT认证 → Handler
中转 API：       API Key 认证 → 并发检查 → 计费检查 → Handler（最短路径，无CORS/日志开销）
```

中转路径刻意绕过所有非必要中间件，确保极致性能。

### 多格式中转

客户端可用四种原生格式中的任一种接入 relay，relay 根据入口路径判断客户端格式，再根据上游渠道类型进行格式转换：

```
入口路径                 客户端格式
/v1/chat/completions    OpenAI Chat Completions
/v1/responses           OpenAI Responses API
/v1/messages            Anthropic Messages
/v1beta/*               Gemini generateContent
```

#### 转换策略：统一中间格式

采用统一中间格式（`InternalRequest`/`InternalResponse`）：

```
客户端格式 → ToInternal() → InternalRequest → FromInternal() → 上游格式
```

只需 8 个转换器（4 个 ToInternal + 4 个 FromInternal），而非直接转换的 12 个。新增 provider 只需加 1 个 `FromInternal()`，直接转换方案要加 4 个。

流式 SSE 逐行转换不走中间格式，直接在 adaptor 层做 provider-specific 的行转换：

```
上游 SSE line → adaptor.ConvertStreamLine(line, clientFormat) → 客户端格式的 SSE line
```

#### 透传

客户端格式与上游格式相同时，直接透传请求和响应，零转换开销。所有格式（含透传）都必须实现 `ParseUsage` 和 `ParseStreamUsage`，relay 需要从响应中提取 usage 做计费统计。

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
    Weight        int        `gorm:"default:1"`
    Enabled       bool       `gorm:"default:true"`
    CooldownUntil *time.Time
    RefreshToken  string     `gorm:"type:text"`                 // AES encrypted (for oauth_token)
    TokenExpiry   *time.Time                                    // access_token expiry
    ClientID      string     `gorm:"type:text"`                 // OAuth client ID
    TokenURL      string     `gorm:"type:text"`                 // OAuth token endpoint
}
```

### Token 模型 (`internal/db/token.go`)

```go
type Token struct {
    Base
    UserID      string `gorm:"size:36;index"`           // 关联 User
    Name        string `gorm:"size:100;not null"`
    Key         string `gorm:"size:100;uniqueIndex;not null"`
    Enabled     bool   `gorm:"default:true"`
    IPWhitelist string `gorm:"type:text"`
    Unlimited   bool   `gorm:"default:false"`
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

## 6. OAuth 凭证自动刷新

参考 upstream 中 Codex CLI 和 Gemini CLI 的 OAuth 实现，relay 支持用 OAuth token 而非静态 API Key 作为上游凭证。

### Codex (OpenAI) OAuth 流程

```
Authorization Code + PKCE:
1. 构建 auth URL: auth.openai.com/oauth/authorize?response_type=code&client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=...&scope=openid+profile+email+offline_access
2. 用户浏览器登录授权，回调获取 code
3. POST auth.openai.com/oauth/token (grant_type=authorization_code) → id_token + access_token + refresh_token
4. POST auth.openai.com/oauth/token (grant_type=urn:ietf:params:oauth:grant-type:token-exchange, requested_token=openai-api-key, subject_token=id_token) → API Key

刷新：POST auth.openai.com/oauth/token (grant_type=refresh_token)
刷新周期：8天，30秒 skew 容差
```

详细流程参考 `docs/reference/cli-auth-reference.md`。

### Gemini (Google) OAuth 流程

```
Authorization Code + PKCE:
1. 构建 auth URL: accounts.google.com/o/oauth2/v2/auth?client_id=681255809395-...&code_challenge=...&scope=cloud-platform+userinfo.email+userinfo.profile
2. 用户浏览器登录授权，回调获取 code
3. POST oauth2.googleapis.com/token (grant_type=authorization_code) → access_token + refresh_token

刷新：POST oauth2.googleapis.com/token (grant_type=refresh_token)
过期检查：5分钟缓冲
```

### Relay 中的刷新逻辑

实际实现在 `internal/relay/account_refresh.go`，签名为：

```go
// EnsureValidCredentials 检查账号凭证是否有效，必要时刷新
func EnsureValidCredentials(account *db.Account, database *gorm.DB) (string, error) {
    if account.CredType == "api_key" || account.CredType == "" {
        return crypto.Decrypt(account.Credentials)
    }
    // OAuth token — 检查过期，使用 singleflight 去重并发刷新
    if account.TokenExpiry != nil && time.Now().After(*account.TokenExpiry) {
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

`refreshOAuthToken` 成功后异步更新数据库并同步更新内存中的 account 状态。使用 `singleflight.Group` 防止同一账号的并发刷新。

## 7. 计费系统

保持现有 PreConsume → Settle + Refund 模式，新增用户维度：

- **Token 绑定用户**：每个 API Key 关联一个 User
- **用户余额**：token_based 套餐的额度从用户余额扣
- **充值码兑换**：充值码增加用户余额（支付接口后期接入）
- **管理员调整**：管理员可直接调整用户余额

## 8. 部署架构

```
单机部署（当前）：
nginx (80/443)
  ├── / → Next.js 静态文件 (/opt/uapi/web/out)
  ├── /api/user/* → Go API Server (127.0.0.1:8080)
  ├── /api/admin/* → Go API Server (127.0.0.1:8080)
  ├── /v1/chat/completions → Go API Server (127.0.0.1:8080)
  ├── /v1/responses → Go API Server (127.0.0.1:8080)
  ├── /v1/messages → Go API Server (127.0.0.1:8080)
  └── /v1beta/* → Go API Server (127.0.0.1:8080)

Go Server (单进程，fasthttp)
PostgreSQL (本地或远程)
```

前端构建：`next build` + `next export` → 静态 HTML/JS/CSS → Nginx 托管。
后端构建：`go build` → 单二进制 → systemd 管理。

详见 `docs/deployment/nginx.md`。

## 9. 不在范围内

- 在线支付接入（支付宝/微信/Stripe）— 后期加
- 邮箱验证 — 后期加
- 多租户/组织 — 后期加
- 分布式部署 — 当前单机足够
- WebSocket 支持 — 当前无需求
