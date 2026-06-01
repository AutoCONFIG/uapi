# UAPI 前端说明

前端位于 `web/`，使用 Next.js 15 App Router 静态导出。生产构建输出在 `web/out`，由 `web/scripts/static-server.mjs` 或外部 nginx 托管。

## 技术栈

- Next.js 15
- React 19
- TypeScript
- 纯 CSS
- lucide-react

`web/next.config.ts` 设置：

```ts
output: "export"
trailingSlash: true
images.unoptimized: true
```

## 路由

公共和用户页面：

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
```

管理页面：

```text
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

用户控制台不显示管理导航。管理控制台不混入用户自助入口。管理员需要调用下游模型 API 时，应创建普通用户账号和用户 API Key。

## API 客户端

主要封装在 `web/lib/api.ts`，类型在 `web/types/api.ts`。

前端对齐的后端 API：

- 用户认证：`POST /api/user/register`、`POST /api/user/login`、`POST /api/user/refresh`。
- 用户资料和设置：`GET /api/user/profile`、`POST /api/user/password`、`POST /api/user/email`。
- API Key：`GET/POST /api/user/keys`、`DELETE /api/user/keys/:keyID`。
- 用量：`GET /api/user/usage`、`GET /api/user/usage/logs`。
- 套餐：`GET /api/user/subscription`、`GET /api/user/plans`、`POST /api/user/redeem`。
- 可见模型：`GET /api/user/models`。
- 管理：channels、accounts、relay-nodes、node-channels、plans、access-policies、users、logs、audit-logs、settings、redeem-codes。
- 导入导出：settings/users 的 import/export，以及 account credential export。
- OAuth：auth-url、callback、complete、status、bind。

登录表单先尝试用户登录，失败后尝试管理员登录。

## 页面职责

- `/overview`：用户概览和快速开始。
- `/keys`：普通用户 API Key 管理，支持 IP 白名单、过期时间、模型限制和 endpoint permission。
- `/usage`：用量汇总和请求日志。
- `/plans`：当前套餐、可公开领取套餐和兑换码。
- `/settings`：用户密码和邮箱修改。
- `/admin/dashboard`：管理概览。
- `/admin/relay-nodes`：Relay 节点和节点-渠道绑定。
- `/admin/channels`：渠道、账号、OAuth 登录、模型同步、账号额度刷新。
- `/admin/users`：用户管理、导入导出。
- `/admin/plans`：套餐和 access policy 组合管理。
- `/admin/logs`：请求日志。
- `/admin/audit-logs`：审计日志。
- `/admin/settings`：系统设置、壁纸、导入导出。

## 渠道管理 UI

当前 UI 把 channel 作为上游访问的顶层对象，账号是 channel 内的 credential。没有独立的账号主导航，也没有第一阶段的管理员侧 API Key 页面。

Channel 负责：

- provider family：`type`
- 协议变体：`api_format`
- 模型目录：`models`
- 模型别名：`model_aliases`
- force stream、affinity、settings 等 channel 级配置

Account 负责：

- API Key 或 OAuth token 凭据
- endpoint
- weight
- enabled
- cooldown
- OAuth metadata 和 token expiry

API Key account 的 endpoint 可编辑。OAuth account 绑定时由后端写入 provider 默认 endpoint，前端不提供手动 endpoint 编辑。

## 权限和模型

普通用户 API Key 可配置：

- `ip_whitelist`
- `expires_at`
- `models`
- `permissions`

Permission 与 Gateway endpoint 对齐：

```text
chat, responses, messages, gemini, images, audio,
embeddings, moderations, realtime, videos
```

用户可见模型来自本地 channel model catalog、model aliases 和用户 active plan policy。下游模型列表不触发上游请求。

## 构建命令

```bash
npm --prefix web install
npm --prefix web run build
npm --prefix web run serve:static
```
