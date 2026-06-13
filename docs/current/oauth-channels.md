# OAuth 渠道

UAPI 支持 API Key 账号和 OAuth 账号。OAuth 账号只允许绑定到匹配的 channel `type + api_format`，绑定后作为 `accounts.cred_type = oauth_token` 保存。

## Provider 注册表

OAuth provider 在 `internal/oauthprovider/provider.go` 注册：

| provider key | label | channel.type | channel.api_format | 默认 endpoint |
| --- | --- | --- | --- | --- |
| `codex` | Codex | `openai` | `codex` | OpenAI Codex API base URL |
| `gemini` | Gemini Code | `gemini` | `gemini_code` | `https://generativelanguage.googleapis.com` |
| `anthropic` | Claude Code | `anthropic` | `claude_code` | `https://api.anthropic.com/v1` |
| `antigravity` | Antigravity | `antigravity` | `antigravity` | `https://cloudcode-pa.googleapis.com` |

标准 API Key 渠道不走 OAuth provider registry。常见标准渠道：

- `openai + standard`
- `openai + responses`
- `gemini + standard`
- `anthropic + standard`

## OAuth 流程

管理端流程由 `internal/admin/oauth_handler.go` 实现：

1. `POST /api/admin/channels/oauth/auth-url` 创建 15 分钟 session、state、PKCE 信息和授权 URL。
2. 管理员完成 provider 授权。
3. `GET /api/admin/channels/oauth/callback` 可接收 UAPI hosted callback。
4. `POST /api/admin/channels/oauth/complete` 可提交 callback URL、authorization code 或 token JSON。
5. `GET /api/admin/channels/oauth/status` 查询 session 状态。
6. `POST /api/admin/channels/oauth/bind` 将已完成 session 绑定为 channel 内的 OAuth account。

安全约束：

- OAuth session 记录创建管理员和创建 IP。
- bind 时必须是同一个管理员。
- callback 会校验发起 IP。
- token URL 必须匹配 provider 默认 host/path，不接受任意 token endpoint。
- OAuth account 只能绑定到匹配的 channel。

## Codex

匹配：

```text
channel.type = openai
channel.api_format = codex
```

特点：

- 支持 manual callback。
- 支持 device flow。
- 默认模型列表来自本地 provider registry。
- token exchange 使用 OpenAI/Codex 客户端逻辑。
- 刷新规则在 Relay 中按 Codex token 生命周期处理。

## Gemini Code

匹配：

```text
channel.type = gemini
channel.api_format = gemini_code
```

特点：

- 使用 Google/Gemini Code OAuth。
- 默认模型和别名在 `internal/oauthprovider/provider.go` 中维护。
- 可同步 Code Assist metadata，用于展示项目和额度相关信息。
- 绑定时 endpoint 使用 `https://generativelanguage.googleapis.com`。

## Claude Code

匹配：

```text
channel.type = anthropic
channel.api_format = claude_code
```

特点：

- 使用 Claude Code OAuth。
- 默认模型列表在 provider registry 中维护。
- 可同步 Anthropic account metadata。
- 绑定时 endpoint 使用 `https://api.anthropic.com/v1`。

## Antigravity

匹配：

```text
channel.type = antigravity
channel.api_format = antigravity
```

特点：

- 使用 Antigravity OAuth。
- 默认模型来自 `internal/relay/provider/antigravity` 的公开模型目录。
- 支持 quota metadata 同步。
- OpenAI-compatible images generation/edit/variation 可转换为 Antigravity `requestType: "image_gen"`。

## 凭据存储和刷新

OAuth account 保存：

- encrypted access credential: `accounts.credentials`
- encrypted refresh token: `accounts.refresh_token`
- token expiry: `accounts.token_expiry`
- client id/token url
- encrypted client secret
- metadata

Relay 执行请求前会调用凭据有效性检查。刷新成功后：

- `all` 模式或本地数据库可用时更新数据库。
- 远程 Relay 先更新内存 account，再 best-effort 调用 Gateway 的 `/internal/account`。

Gateway 不可达时不会持久重试，这是当前已知边界。

## 上游源码对齐

OAuth 行为需要对齐仓库中的上游客户端源码，而不是靠公开 API 猜测：

```text
upstream/codex/
upstream/gemini-cli/
upstream/claude-code/
```

修改 OAuth 授权、刷新、metadata、请求 shaping 前，应先阅读对应 provider 的本地 upstream 代码和当前 provider adapter。
