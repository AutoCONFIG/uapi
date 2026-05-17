# CLI Auth 认证流程参考

本文档记录上游 AI CLI 工具的认证流程细节，作为 UAPI 项目 OAuth 集成的参考。

> 注意：Codex 和 Gemini 的 OAuth 已有代码骨架（`internal/relay/provider/openai/auth.go`、
> `internal/relay/provider/gemini/auth.go`）。Kilocode 仅为参考，当前无集成计划。

---

## 1. Codex (OpenAI)

### 支持的认证方式

| 方式 | 说明 |
|---|---|
| 浏览器 OAuth2+PKCE | 本地 callback server，打开浏览器认证 |
| 设备码 | 无浏览器环境，终端输入验证码 |
| API Key | 直接提供 `sk-proj-...` 格式的 key |

### 关键常量

| 常量 | 值 |
|---|---|
| Client ID | `app_EMoamEEZ73f0CkXaXp7hrann` |
| Issuer | `https://auth.openai.com` |
| 默认 Callback 端口 | `1455` |
| Scopes | `openid profile email offline_access api.connectors.read api.connectors.invoke` |
| 主动刷新间隔 | 8 天 |
| 设备码超时 | 15 分钟 |
| 撤销超时 | 10 秒 |

### 浏览器 OAuth2 流程

#### Step 1: 生成 PKCE

- `code_verifier`: 64 随机字节，base64url 编码（无 padding）
- `code_challenge`: `BASE64URL_NO_PAD(SHA256(code_verifier))`
- `code_challenge_method`: `S256`

#### Step 2: 打开浏览器

```
GET https://auth.openai.com/oauth/authorize?
  response_type=code&
  client_id=app_EMoamEEZ73f0CkXaXp7hrann&
  redirect_uri=http://localhost:1455/auth/callback&
  scope=openid profile email offline_access api.connectors.read api.connectors.invoke&
  code_challenge={challenge}&
  code_challenge_method=S256&
  id_token_add_organizations=true&
  codex_cli_simplified_flow=true&
  state={32字节随机base64url}&
  originator=codex_cli_rs
```

#### Step 3: 交换授权码获取 Token

```
POST https://auth.openai.com/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&
code={authorization_code}&
redirect_uri=http://localhost:1455/auth/callback&
client_id=app_EMoamEEZ73f0CkXaXp7hrann&
code_verifier={pkce_code_verifier}
```

响应：
```json
{
  "id_token": "<JWT>",
  "access_token": "<JWT>",
  "refresh_token": "<opaque string>"
}
```

#### Step 4: Token-Exchange Grant（获取 API-key-style token）

```
POST https://auth.openai.com/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=urn:ietf:params:oauth:grant-type:token-exchange&
client_id=app_EMoamEEZ73f0CkXaXp7hrann&
requested_token=openai-api-key&
subject_token={id_token}&
subject_token_type=urn:ietf:params:oauth:token-type:id_token
```

响应：
```json
{
  "access_token": "<API-key-style token>"
}
```

此 `access_token` 存储为 `OPENAI_API_KEY`。

### 设备码流程

#### Step 1: 请求用户码

```
POST https://auth.openai.com/api/accounts/deviceauth/usercode
Content-Type: application/json

{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"}
```

响应：
```json
{
  "device_auth_id": "...",
  "user_code": "...",
  "interval": "5"
}
```

用户访问 `https://auth.openai.com/codex/device` 输入 user_code。

#### Step 2: 轮询授权

```
POST https://auth.openai.com/api/accounts/deviceauth/token
Content-Type: application/json

{"device_auth_id": "...", "user_code": "..."}
```

- HTTP 403/404：继续轮询，等待 `interval` 秒
- 成功响应：
```json
{
  "authorization_code": "...",
  "code_challenge": "...",
  "code_verifier": "..."
}
```

注意：PKCE code 由服务端提供！

#### Step 3: 交换 Token

与浏览器流程相同，使用 `redirect_uri = {issuer}/deviceauth/callback` 和轮询返回的 PKCE 参数。然后执行同样的 token-exchange grant。

### Token 刷新

```
POST https://auth.openai.com/oauth/token
Content-Type: application/json

{
  "client_id": "app_EMoamEEZ73f0CkXaXp7hrann",
  "grant_type": "refresh_token",
  "refresh_token": "..."
}
```

响应（所有字段可选，仅非空值需更新）：
```json
{
  "id_token": "...",
  "access_token": "...",
  "refresh_token": "..."
}
```

**主动刷新触发条件**（满足任一）：
- JWT `exp` 已过期
- `last_refresh` 超过 8 天

**401 错误分类**：

| 错误码 | 含义 | 是否永久 |
|---|---|---|
| `refresh_token_expired` | Token 过期 | 永久 |
| `refresh_token_reused` | Token 被重复使用 | 永久 |
| `refresh_token_invalidated` | Token 被撤销 | 永久 |
| 其他 401 | 其他原因 | 永久（缓存不重试） |

**401 Recovery 状态机**：
1. Reload：重新从存储读取（可能其他进程已刷新）
2. Refresh：调用刷新接口
3. Done：无法恢复

### Token 撤销

```
POST https://auth.openai.com/oauth/revoke
Content-Type: application/json

// 撤销 refresh token（优先）：
{"token": "<refresh_token>", "token_type_hint": "refresh_token", "client_id": "app_EMoamEEZ73f0CkXaXp7hrann"}

// 撤销 access token（仅当无 refresh token 时）：
{"token": "<access_token>", "token_type_hint": "access_token"}
```

注意：撤销 access token 时**不包含** `client_id`。

### 存储格式 (auth.json)

文件路径：`$CODEX_HOME/auth.json`（权限 0o600）

```json
{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": "sk-...",
  "tokens": {
    "id_token": "<原始 JWT 字符串>",
    "access_token": "<JWT 字符串>",
    "refresh_token": "<opaque string>",
    "account_id": "acct_xxx"
  },
  "last_refresh": "2026-04-19T10:30:00Z",
  "agent_identity": null
}
```

- `auth_mode`: `"chatgpt"` | `"api_key"` | `"chatgpt_auth_tokens"`
- `id_token`: 存储原始 JWT 字符串（非解析后的对象）
- `OPENAI_API_KEY`: token-exchange grant 获取的 API-key-style token

### API 调用 Headers

| Header | 值 | 条件 |
|---|---|---|
| `Authorization` | `Bearer {access_token}` | 始终 |
| `ChatGPT-Account-ID` | `{account_id}` | 当 account_id 存在时 |
| `X-OpenAI-Fedramp` | `true` | 当 FedRAMP 账户时 |

JWT Claims（`https://api.openai.com/auth` 命名空间下）：
- `chatgpt_account_id` → account_id
- `chatgpt_plan_type` → 计划类型
- `chatgpt_account_is_fedramp` → 是否 FedRAMP

### 环境变量覆盖

| 变量 | 用途 |
|---|---|
| `CODEX_REFRESH_TOKEN_URL_OVERRIDE` | 覆盖刷新 token 的 URL |
| `CODEX_REVOKE_TOKEN_URL_OVERRIDE` | 覆盖撤销 token 的 URL |

---

## 2. Gemini CLI (Google)

### 支持的认证方式

| 方式 | 枚举值 | 说明 |
|---|---|---|
| Google OAuth2（浏览器） | `LOGIN_WITH_GOOGLE` | 本地 callback server |
| Google OAuth2（手动码） | `LOGIN_WITH_GOOGLE` | NO_BROWSER=true 时使用 PKCE |
| Gemini API Key | `USE_GEMINI` | 直接提供 API key |
| Vertex AI | `USE_VERTEX_AI` | Google API key 或 Vertex AI |
| Compute ADC | `COMPUTE_ADC` | GCE 元数据服务器 |
| Gateway | `GATEWAY` | 网关代理 |

### 关键常量

| 常量 | 值 |
|---|---|
| Client ID | `<GEMINI_CLIENT_ID>` (见上游源码) |
| Client Secret | `<GEMINI_CLIENT_SECRET>` (见上游源码) |
| Scopes | `cloud-platform userinfo.email userinfo.profile` |
| API Base | `https://cloudcode-pa.googleapis.com/v1internal` |
| Token 过期检测缓冲 | 5 分钟 |

### 浏览器 OAuth2 流程

#### 授权 URL

```
GET https://accounts.google.com/o/oauth2/v2/auth?
  client_id=<GEMINI_CLIENT_ID>&
  redirect_uri=http://127.0.0.1:{port}/oauth2callback&
  access_type=offline&
  scope=https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile&
  state={64位hex随机字符串}&
  response_type=code
```

注意：浏览器流程**不使用** PKCE。

#### Token 交换

```
POST https://oauth2.googleapis.com/token
Content-Type: application/x-www-form-urlencoded

code={authorization_code}&
grant_type=authorization_code&
client_id=<GEMINI_CLIENT_ID>&
client_secret=<GEMINI_CLIENT_SECRET>&
redirect_uri=http://127.0.0.1:{port}/oauth2callback
```

响应：
```json
{
  "access_token": "ya29...",
  "refresh_token": "1//...",
  "token_type": "Bearer",
  "expiry_date": 1710000000000,
  "scope": "https://www.googleapis.com/auth/cloud-platform ..."
}
```

#### 回调后重定向

- 成功：→ `https://developers.google.com/gemini-code-assist/auth_success_gemini`
- 失败：→ `https://developers.google.com/gemini-code-assist/auth_failure_gemini`

### 手动码流程 (NO_BROWSER)

与浏览器流程的区别：

| 参数 | 浏览器流程 | 手动码流程 |
|---|---|---|
| `redirect_uri` | `http://127.0.0.1:{port}/oauth2callback` | `https://codeassist.google.com/authcode` |
| `code_challenge_method` | 不设置 | `S256` |
| `code_challenge` | 不设置 | PKCE 派生 |
| PKCE | 不使用 | 完整 PKCE S256 |

Token 交换时额外包含 `code_verifier` 参数。

### Token 刷新

```
POST https://oauth2.googleapis.com/token
Content-Type: application/x-www-form-urlencoded

refresh_token={refresh_token}&
client_id=<GEMINI_CLIENT_ID>&
client_secret=<GEMINI_CLIENT_SECRET>&
grant_type=refresh_token
```

`google-auth-library` 的 `OAuth2Client` 自动处理刷新，通过 `tokens` 事件监听器保存新 token。

### 存储格式

#### 文件存储（默认）：`~/.gemini/oauth_creds.json`

```json
{
  "access_token": "ya29...",
  "refresh_token": "1//...",
  "token_type": "Bearer",
  "expiry_date": 1710000000000,
  "scope": "https://www.googleapis.com/auth/cloud-platform ..."
}
```

#### 加密存储（`GEMINI_FORCE_ENCRYPTED_FILE_STORAGE=true`）

使用 `HybridTokenStorage`：OS Keychain → 加密文件回退。格式：
```json
{
  "serverName": "main-account",
  "token": {
    "accessToken": "ya29...",
    "refreshToken": "1//...",
    "tokenType": "Bearer",
    "scope": "...",
    "expiresAt": 1710000000000
  },
  "updatedAt": 1710000000000
}
```

#### API Key 存储

```json
{
  "serverName": "default-api-key",
  "token": {
    "accessToken": "AIza...",
    "tokenType": "ApiKey"
  },
  "updatedAt": 1710000000000
}
```

### API Key 使用方式

由 `GEMINI_API_KEY_AUTH_MECHANISM` 环境变量控制：

| 值 | 传递方式 |
|---|---|
| `x-goog-api-key`（默认） | 作为 `x-goog-api-key` 查询参数 |
| `bearer` | 作为 `Authorization: Bearer {api_key}` header |

### Code Assist API

**基础 URL**: `https://cloudcode-pa.googleapis.com/v1internal`

环境变量覆盖：
- `CODE_ASSIST_ENDPOINT` → 替换 base URL
- `CODE_ASSIST_API_VERSION` → 替换 API 版本

**关键 API 方法**：

| 方法 | HTTP | URL |
|---|---|---|
| streamGenerateContent | POST | `:streamGenerateContent?alt=sse` |
| generateContent | POST | `:generateContent` |
| countTokens | POST | `:countTokens` |
| onboardUser | POST | `:onboardUser` |
| loadCodeAssist | POST | `:loadCodeAssist` |
| fetchAdminControls | POST | `:fetchAdminControls` |
| getOperation | GET | `/{operation_name}` |

**请求 Headers**：
```
Content-Type: application/json
Authorization: Bearer {access_token}
User-Agent: GeminiCLI/{version}/{model} ({platform}; {arch}; {surface})
```

### 初始化/Onboarding 流程

1. **loadCodeAssist**：获取用户 tier 和 project ID
   ```json
   POST :loadCodeAssist
   {
     "cloudaicompanionProject": "{project_id}",
     "metadata": {"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
   }
   ```

2. **检查 tier**：如果 `currentTier` 存在且有 project → 已 onboard

3. **onboardUser**：新用户注册（可能是 Long-Running Operation）
   ```json
   POST :onboardUser
   {"tierId": "free-tier", "metadata": {...}}
   ```

4. **轮询 LRO**：如果 `done: false`，每 5 秒轮询 `GET /{operation_name}`

---

## 3. Kilocode

### 两套独立的认证系统

Kilocode 有**两套不统一**的认证系统：

1. **Kilo Gateway Device Auth**：自有系统，对 `api.kilo.ai`
2. **OpenCode Account Device Auth**：通用系统，对用户指定服务器

### Kilo Gateway Device Code 流程

#### 关键常量

| 常量 | 值 |
|---|---|
| API Base URL | `https://api.kilo.ai` |
| 轮询间隔 | 3 秒 |
| Token 有效期 | 1 年 |
| 环境变量覆盖 | `KILO_API_URL` |

#### Step 1: 发起设备认证

```
POST https://api.kilo.ai/api/device-auth/codes
Content-Type: application/json
```

注意：**无请求体**。

响应（200）：
```json
{
  "code": "string",
  "verificationUrl": "string",
  "expiresIn": 300
}
```

429 = 请求过多。

#### Step 2: 轮询授权状态

```
GET https://api.kilo.ai/api/device-auth/codes/{code}
```

| HTTP 状态码 | 含义 |
|---|---|
| 202 | 待处理 (pending) |
| 200 | 已批准 |
| 403 | 被拒绝 (denied) |
| 410 | 已过期 (expired) |

200 响应体：
```json
{
  "status": "approved",
  "token": "string",
  "userEmail": "string"
}
```

#### Token 特点

- `access` 和 `refresh` 设置为**同一个 token 值**
- 有效期 1 年
- **无刷新机制**

### OpenCode Account Device Code 流程

#### Step 1: 请求设备码

```
POST {server}/auth/device/code
Accept: application/json
Content-Type: application/json

{"client_id": "opencode-cli"}
```

响应：
```json
{
  "device_code": "string",
  "user_code": "string",
  "verification_uri_complete": "string",
  "expires_in": 300,
  "interval": 5
}
```

#### Step 2: 轮询 Token

```
POST {server}/auth/device/token
Accept: application/json
Content-Type: application/json

{
  "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
  "device_code": "string",
  "client_id": "opencode-cli"
}
```

成功响应：
```json
{
  "access_token": "string",
  "refresh_token": "string",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

错误码（RFC 8628）：

| error | 含义 |
|---|---|
| `authorization_pending` | 继续轮询 |
| `slow_down` | 增加间隔 |
| `expired_token` | 停止 |
| `access_denied` | 停止 |

#### Token 刷新

```
POST {account.url}/auth/device/token
Accept: application/json
Content-Type: application/json

{
  "grant_type": "refresh_token",
  "refresh_token": "string",
  "client_id": "opencode-cli"
}
```

响应：
```json
{
  "access_token": "string",
  "refresh_token": "string",
  "expires_in": 3600
}
```

**Eager Refresh**：token 过期前 5 分钟主动刷新。

### 存储格式

#### auth.json：`~/.local/share/kilo/auth.json`

```json
{
  "kilo": {
    "type": "oauth",
    "refresh": "string",
    "access": "string",
    "expires": 1735689600000,
    "accountId": "optional-string",
    "enterpriseUrl": "optional-string"
  },
  "some-provider": {
    "type": "api",
    "key": "string",
    "metadata": {"key": "value"}
  },
  "another-provider": {
    "type": "wellknown",
    "key": "ENV_VAR_NAME",
    "token": "actual_token_value"
  }
}
```

三种类型：
- **oauth**: refresh, access, expires, accountId?, enterpriseUrl?
- **api**: key, metadata?
- **wellknown**: key (环境变量名), token (实际值)

#### mcp-auth.json：`~/.local/share/kilo/mcp-auth.json`

```json
{
  "server-name": {
    "tokens": {
      "accessToken": "string",
      "refreshToken": "optional",
      "expiresAt": 1735689600.0,
      "scope": "optional"
    },
    "clientInfo": {
      "clientId": "string",
      "clientSecret": "optional",
      "clientIdIssuedAt": 1735689600.0,
      "clientSecretExpiresAt": 1735689600.0
    },
    "codeVerifier": "optional",
    "oauthState": "optional",
    "serverUrl": "optional"
  }
}
```

### API 调用 Headers

#### Kilo Gateway Provider

基础 URL：`https://api.kilo.ai/openrouter/`

| Header | 值 | 条件 |
|---|---|---|
| `Authorization` | `Bearer {apiKey}` | 始终 |
| `X-KILOCODE-EDITORNAME` | `Kilo CLI [{version}]` | 始终 |
| `X-KILOCODE-ORGANIZATIONID` | `{orgId}` | 可选 |
| `X-KILOCODE-TASKID` | `{taskId}` | 可选 |
| `X-KILOCODE-PROJECTID` | `{projectId}` | orgId 和 projectId 都有时 |
| `X-KILOCODE-MACHINEID` | `{machineId}` | 可选 |
| `X-KILOCODE-FEATURE` | `{feature}` | KILOCODE_FEATURE 环境变量 |

#### Kilo Profile API

```
GET https://api.kilo.ai/api/profile
Authorization: Bearer {token}

GET https://api.kilo.ai/api/profile/balance
Authorization: Bearer {token}
x-kilocode-organizationid: {orgId}  // 可选

GET https://api.kilo.ai/api/defaults
Authorization: Bearer {token}        // 可选
```

### MCP OAuth 流程

Callback 端口：`19876`，路径：`/mcp/oauth/callback`

动态注册客户端元数据：
```json
{
  "redirect_uris": ["http://127.0.0.1:19876/mcp/oauth/callback"],
  "client_name": "Kilo",
  "client_uri": "https://kilo.ai",
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

---

## 三者对比速查

| | Codex | Gemini CLI | Kilocode |
|---|---|---|---|
| **主要认证** | OAuth2+PKCE / 设备码 / API Key | Google OAuth2 / API Key / Vertex AI | 设备码 (Gateway) / 设备码 (Account) / API Key |
| **Token 类型** | JWT access + refresh + id_token | Google OAuth2 access + refresh | Bearer token (access=refresh, 1年) |
| **刷新机制** | 8天主动 / 401被动 | google-auth-library 自动 | 5分钟提前 (Account); 无刷新 (Gateway) |
| **存储** | auth.json / Keyring | ~/.gemini/oauth_creds.json / Keychain | ~/.local/share/kilo/auth.json / SQLite |
| **API Header** | Bearer + Account-ID + Fedramp | Bearer 或 x-goog-api-key | Bearer + 多个 X-KILOCODE-* headers |
| **PKCE** | 浏览器: S256; 设备码: 服务端提供 | 浏览器: 无; NO_BROWSER: S256 | N/A |
