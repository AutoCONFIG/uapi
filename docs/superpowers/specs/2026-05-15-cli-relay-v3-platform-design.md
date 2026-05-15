---
title: cli-relay v3 — API 中转平台设计
date: 2026-05-15
status: draft
---

# cli-relay v3 — API 中转平台设计文档

## 1. 项目定位

cli-relay 是一个**面向公众的 AI API 中转平台**。用户注册账号后购买套餐，获取 API Key 调用 OpenAI/Anthropic/Gemini 等上游服务。管理员管理渠道、账号池、计费等后台功能。

核心能力：
- 透明代理 + 格式转换（OpenAI 兼容格式 → 上游原生格式）
- 账号池管理（加权轮询 + 故障冷却 + OAuth 自动刷新）
- 双模式计费（次数窗口限额 + Token 额度扣费）
- 用户注册/登录/套餐购买/API Key 管理
- 管理员后台（渠道/账号/用户/计费管理）
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
│    ├── /api/v1/*   → 后端用户 API             │
│    ├── /api/admin/*→ 后端管理 API             │
│    └── /v1/*       → 后端中转 API (极致性能)   │
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
| shadcn/ui | 最新 | Stripe 风格天然契合，组件完全可控，不锁定 |
| Tailwind CSS | 4 | 现代 CSS，与 shadcn 深度集成 |
| Zustand | 5 | 轻量状态管理 |
| TanStack Query | 5 | 服务端数据缓存和同步 |

设计风格：**Stripe 风格** — 亮色白底，紫蓝渐变点缀，卡片式布局，大量留白，信息密度适中。

UI 当前阶段先保证功能齐全和操作逻辑清晰，后期可换皮肤不改骨架。

### 页面结构

```
web/src/
├── app/
│   ├── (auth)/                    # 认证页面（无侧边栏）
│   │   ├── login/page.tsx         # 登录
│   │   ├── register/page.tsx      # 注册
│   │   └── layout.tsx
│   ├── (dashboard)/               # 登录后（带侧边栏）
│   │   ├── layout.tsx            # 侧边栏 + 顶栏
│   │   ├── overview/page.tsx     # 总览：用量 + 快速开始代码
│   │   ├── keys/page.tsx         # API 密钥管理
│   │   ├── usage/page.tsx        # 用量统计（图表 + 明细）
│   │   ├── plans/page.tsx        # 套餐浏览/购买
│   │   ├── settings/page.tsx     # 个人设置（密码/邮箱）
│   │   └── ...
│   ├── layout.tsx                # 根布局（字体、主题、Provider）
│   └── globals.css
├── components/
│   ├── ui/                        # shadcn/ui 组件
│   ├── layout/                    # 侧边栏、顶栏、导航
│   └── shared/                    # 通用业务组件
├── lib/
│   ├── api.ts                    # API 客户端（fetch 封装 + JWT 注入）
│   ├── auth.ts                   # JWT 存储/刷新/重定向
│   └── utils.ts                  # 工具函数
└── types/                         # TypeScript 类型定义
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

### 目录结构

```
internal/
├── server/
│   ├── server.go              # Server 初始化和生命周期
│   ├── router.go              # 路由注册（替代 switch）
│   └── middleware.go          # CORS, 请求日志, 限流
│
├── relay/                     # 核心中转引擎（保留现有实现）
│   ├── handler.go             # 精简为调度逻辑
│   ├── pool.go                # 加权轮询号池
│   ├── affinity.go            # 渠道亲和缓存
│   ├── billing.go             # 预消费/结算/退款
│   ├── concurrency.go         # 并发限制
│   ├── streaming.go           # SSE 流式转发
│   ├── sse_reader.go          # SSE 读取器
│   ├── stream_converter.go    # 流式→非流式转换
│   └── provider/              # 上游适配器
│       ├── types.go           # Adaptor 接口 + 凭证提取
│       ├── openai/
│       │   ├── adaptor.go     # 请求/响应透传
│       │   ├── responses.go   # Responses API ↔ Chat 转换
│       │   └── auth.go        # OpenAI OAuth（Codex 流程）
│       ├── anthropic/
│       │   ├── adaptor.go     # OpenAI ↔ Anthropic 格式转换
│       │   ├── streaming.go   # Anthropic SSE → OpenAI SSE
│       │   └── auth.go        # Anthropic OAuth（如适用）
│       └── gemini/
│           ├── adaptor.go     # OpenAI ↔ Gemini 格式转换
│           ├── streaming.go   # Gemini SSE → OpenAI SSE
│           └── auth.go        # Google OAuth（Gemini CLI 流程）
│
├── user/                      # 用户系统
│   ├── handler.go             # HTTP handler
│   ├── service.go             # 业务逻辑
│   └── dto.go                 # 请求/响应结构体
│
├── admin/                     # 管理后台
│   ├── handler.go             # 路由分发
│   ├── channel_handler.go     # 渠道 CRUD
│   ├── account_handler.go     # 账号 CRUD
│   ├── token_handler.go       # 令牌 CRUD
│   ├── plan_handler.go        # 套餐 CRUD
│   ├── user_handler.go        # 用户管理
│   ├── log_handler.go         # 日志查询
│   └── dto.go                 # 请求/响应结构体
│
├── auth/                      # 认证系统
│   ├── jwt.go                 # JWT 生成/验证（双系统：admin + user）
│   └── middleware.go          # JWT 认证中间件
│
├── db/                        # 数据模型
│   ├── db.go                  # 数据库初始化 + AutoMigrate
│   ├── channel.go
│   ├── account.go
│   ├── token.go
│   ├── plan.go
│   ├── user.go                # 用户模型
│   ├── user_token.go          # 用户-令牌关联
│   ├── log.go
│   └── audit_log.go
│
├── crypto/                    # AES-256-GCM 加密
└── config/
    └── config.go              # 配置
```

### 路由设计

```
# ── 用户侧 API（用户 JWT 认证） ──
POST   /api/v1/auth/register          # 注册
POST   /api/v1/auth/login             # 登录
POST   /api/v1/auth/refresh           # 刷新 JWT

GET    /api/v1/user/profile           # 个人信息
PUT    /api/v1/user/password          # 修改密码
PUT    /api/v1/user/email             # 修改邮箱

GET    /api/v1/user/keys              # 我的 API 密钥列表
POST   /api/v1/user/keys              # 创建 API 密钥
DELETE /api/v1/user/keys/:id          # 删除 API 密钥

GET    /api/v1/user/usage             # 用量统计（汇总）
GET    /api/v1/user/usage/logs        # 用量明细（分页）

GET    /api/v1/plans                  # 套餐列表（公开）
GET    /api/v1/user/subscription      # 我的当前套餐
POST   /api/v1/user/subscription      # 购买套餐
POST   /api/v1/user/redeem            # 兑换充值码

# ── 管理员 API（admin JWT 认证） ──
POST   /api/admin/login              # 管理员登录
GET    /api/admin/init-status         # 初始化状态
POST   /api/admin/setup              # 首次初始化

GET    /api/admin/dashboard           # 仪表盘统计
CRUD   /api/admin/channels            # 渠道管理
CRUD   /api/admin/accounts            # 账号管理
CRUD   /api/admin/tokens              # 令牌管理
CRUD   /api/admin/plans               # 套餐管理
CRUD   /api/admin/users               # 用户管理
GET    /api/admin/logs                # 请求日志
GET    /api/admin/audit-logs          # 审计日志

# ── 中转 API（API Key 认证，极致性能路径） ──
ANY    /v1/*                          # OpenAI 兼容转发
```

### 中间件链

```
用户/管理 API：  CORS → 请求日志 → JWT认证 → 限流 → Handler
中转 API：       API Key 认证 → 并发检查 → 计费检查 → Handler（最短路径，无CORS/日志开销）
```

中转路径刻意绕过所有非必要中间件，确保极致性能。

## 5. 数据模型

### 新增 User 模型

```go
type User struct {
    Base                                          // uuid PK, created_at, updated_at, deleted_at
    Email        string  `gorm:"uniqueIndex;not null"`
    Username     string  `gorm:"uniqueIndex;not null"`
    PasswordHash string  `gorm:"not null"`
    Status       string  `gorm:"default:active"`     // active, disabled
    Balance      int64   `gorm:"default:0"`          // 余额（token 单位）
}
```

### 扩展 Account 模型

```go
type Account struct {
    Base
    ChannelID     string
    Name          string
    Credentials   string       // AES 加密存储
    CredType      string       // "api_key" | "oauth_token"
    Weight        int          `gorm:"default:1"`
    Enabled       bool         `gorm:"default:true"`
    CooldownUntil *time.Time
    // OAuth 字段（CredType=oauth_token 时使用）
    RefreshToken  string       // AES 加密
    TokenExpiry   *time.Time   // access_token 过期时间
    ClientID      string       // OAuth client ID
    TokenURL      string       // OAuth token endpoint
}
```

### Token 关联用户

```go
type Token struct {
    Base
    UserID       string       // 【新】关联 User
    Name         string
    Key          string       `gorm:"uniqueIndex"`
    PlanID       string
    IPWhitelist  string
    Unlimited    bool
    Enabled      bool         `gorm:"default:true"`
}
```

### 充值码（预留）

```go
type RedeemCode struct {
    Base
    Code         string  `gorm:"uniqueIndex;not null"`
    Value        int64   // 充值额度
    UsedBy       *string // 使用者 User ID
    UsedAt       *time.Time
    Status       string  // active, used, expired
    ExpiresAt    time.Time
}
```

其他模型（Channel, Plan, TokenPlan, Log, AuditLog）保持不变。

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

```go
// 在 relay handler 发请求前调用
func (a *Account) EnsureValidCredentials() (string, error) {
    if a.CredType == "api_key" {
        return a.DecryptedCredentials, nil
    }
    // OAuth token 路径
    if a.TokenExpiry != nil && time.Now().After(*a.TokenExpiry) {
        newToken, newRefresh, newExpiry, err := refreshOAuthToken(a)
        if err != nil {
            // 刷新失败 → 账号 cooldown，换下一个
            return "", ErrTokenRefreshFailed
        }
        // 更新数据库（异步，不阻塞请求）
        go updateAccountTokens(a.ID, newToken, newRefresh, newExpiry)
        return newToken, nil
    }
    return a.DecryptedCredentials, nil
}
```

## 7. 计费系统

保持现有 PreConsume → Settle + Refund 模式，新增用户维度：

- **Token 绑定用户**：每个 API Key 关联一个 User
- **用户余额**：token_based 套餐的额度从用户余额扣
- **充值码兑换**：充值码增加用户余额（支付接口后期接入）
- **管理员调整**：管理员可直接调整用户余额

## 8. 保留的核心组件

以下组件从现有代码直接迁移，不重写：

| 组件 | 原位置 | 说明 |
|------|--------|------|
| Adaptor 接口 | relay/types/adaptor.go | 9 方法定义完整 |
| OpenAI 适配器 | relay/openai/ | 透传 + Responses API 转换 |
| Anthropic 适配器 | relay/anthropic/ | 格式转换 + SSE 流式转换 |
| Gemini 适配器 | relay/gemini/ | 格式转换 + SSE 流式转换 |
| 加权轮询号池 | relay/pool.go | 平滑加权轮询算法 |
| 亲和缓存 | relay/affinity.go | Token+Model → Channel 绑定 |
| 预消费计费 | relay/billing.go | PreConsume/Settle/Refund |
| 并发限制 | relay/concurrency.go | 按令牌限流 |
| SSE 读取器 | relay/sse_reader.go | chan-based 实时推送 |
| 流式转发 | relay/streaming.go | 逐行转换 + 透传 |
| 流转非流 | relay/stream_converter.go | SSE 缓冲转 JSON |
| AES-256-GCM | crypto/ | 凭证加密 |
| JWT 认证 | auth/ | 扩展为双系统 |

## 9. 需要修复的已知问题

| 问题 | 位置 | 修复方案 |
|------|------|----------|
| fasthttp API 误用 | adaptor SetupRequestHeader | 用 req.Header 替代 req.Request.Header |
| Anthropic 丢弃 thinking_delta | anthropic/streaming.go | 转发为 reasoning_content 字段 |
| Gemini tool call ID 不稳定 | gemini/streaming.go | 基于 content hash 生成确定性 ID |
| 手动 switch 路由 | server/server.go | 替换为路由器 |
| 无中间件链 | server/server.go | 实现中间件管道 |
| admin handler 过大 | admin/admin.go | 按资源拆分文件 |
| 无 DTO | admin/admin.go | 请求/响应结构体 + JSON tag 校验 |
| 亲和缓存 reaper 可能泄漏 | relay/affinity.go | 改用 time.AfterFunc 或 sync.Map |

## 10. 部署架构

```
单机部署（当前）：
nginx (80/443)
  ├── / → Next.js 静态文件 (/var/www/cli-relay/web/out)
  ├── /api/v1/* → Go API Server (127.0.0.1:8080)
  ├── /api/admin/* → Go API Server (127.0.0.1:8080)
  └── /v1/* → Go API Server (127.0.0.1:8080)

Go Server (单进程，fasthttp)
PostgreSQL (本地或远程)
```

前端构建：`next build` + `next export` → 静态 HTML/JS/CSS → Nginx 托管。
后端构建：`go build` → 单二进制 → systemd 管理。

## 11. 不在范围内

- 在线支付接入（支付宝/微信/Stripe）— 后期加
- 邮箱验证 — 后期加
- 多租户/组织 — 后期加
- 分布式部署 — 当前单机足够
- WebSocket 支持 — 当前无需求
