# upstream/chatgpt2api ChatGPT 网页逆向规范

本文档整理 `upstream/chatgpt2api` 中与 ChatGPT 网页端逆向直接相关的实现。范围仅包括 `chatgpt.com`、`auth.openai.com`、`sentinel.openai.com` 的网页认证、Sentinel/PoW、Conversation SSE、网页图片生成/编辑、文件上传下载、搜索和可编辑文件导出链路。不包含本项目对外 OpenAI 兼容 API、账号池调度、缓存、日志、存储、前端页面、管理后台，也不包含非网页端分支。

## 0. 完整性确认

已按源码反查以下网页逆向代码路径，并在本文覆盖其获取细节、请求/响应形态和关键实现方式：

| 代码路径 | 覆盖章节 | 覆盖状态 |
|:--|:--|:--|
| `OpenAIBackendAPI.__init__` / `_build_fp` / `_headers` | 2 | 已覆盖浏览器指纹、基础 headers、target path/route。 |
| `_bootstrap` / `parse_pow_resources` | 2.2、3 | 已覆盖首页预热、脚本和 `data-build` 提取、PoW 资源 fallback。 |
| `_get_chat_requirements` / `_build_requirements` / `utils.pow` | 3 | 已覆盖 requirements 请求、legacy token、proof token、Arkose/Turnstile 分支。 |
| `utils.turnstile` | 3.3 | 已覆盖 `dx` 解码、XOR、指令解释器和 token 输出。 |
| `_conversation_payload` / `stream_conversation` | 4 | 已覆盖普通登录/匿名 Conversation SSE 请求体和 headers。 |
| `iter_sse_payloads` / `iter_conversation_payloads` | 4.3、4.4 | 已覆盖 SSE 读取、patch、文本状态机和图片指针状态。 |
| `_upload_image` / `_upload_editable_base64_image` | 5 | 已覆盖 `/backend-api/files`、Blob PUT、uploaded 标记和 library 上传差异。 |
| `utils.helper` 图片输入解析 | 5.4 | 已覆盖 data URL、HTTP(S) 远程图、base64、Anthropic/OpenAI 风格图片对象。 |
| `_prepare_image_conversation` / `_start_image_generation` | 6 | 已覆盖网页图片生成/编辑 prepare 和 `/backend-api/f/conversation` SSE。 |
| `_poll_image_results` / `_query_backend_tasks` / `_resolve_image_urls` | 6.5、6.6、7 | 已覆盖 conversation 轮询、tasks 错误、图片 URL 解析和下载。 |
| `_get_me` / `_get_conversation_init` / `_get_default_account` / `list_models` | 8 | 已覆盖模型、用户、套餐、图片额度和恢复时间获取。 |
| `_prepare_search_conversation` / `_run_search_conversation` / `_extract_search_result` | 9 | 已覆盖网页搜索 conversation 和结果/来源提取。 |
| 可编辑文件导出相关 `_prepare_editable_*` / `_run_editable_*` / `_resolve_editable_download_url` | 10 | 已覆盖 thinking 模型、library 附件、artifact 提取和多路径下载。 |
| `oauth_login_service` / `utils.pkce` | 11 | 已覆盖 OAuth + PKCE、state/session、code 换 token。 |
| `account_service` refresh/password login | 11.3、11.4 | 已覆盖 refresh_token 刷新、密码登录、错误识别和 JWT 信息提取。 |
| `utils.sentinel` / `openai_register` 登录注册 Sentinel | 12、13 | 已覆盖 Sentinel req、登录/注册 flow、OTP、注册账号资料和 token 兑换。 |

按要求明确排除：

| 排除内容 | 原因 |
|:--|:--|
| 本项目对外 `/v1/*` 兼容 API 包装 | 属于项目业务封装，不是 ChatGPT 网页逆向本体。 |
| 账号池选择、并发槽位、日志、缓存、存储、前端页面 | 属于业务和管理逻辑。 |
| 非 `chatgpt.com` 网页端的独立分支 | 用户要求只整理网页逆向。 |

### 0.1 上游端点清单

从源码字符串抽取后，本文覆盖的网页逆向端点如下：

| 端点 | 章节 |
|:--|:--|
| `GET https://chatgpt.com/` | 2.2 |
| `POST /backend-api/sentinel/chat-requirements` | 3 |
| `POST /backend-anon/sentinel/chat-requirements` | 3 |
| `POST /backend-api/conversation` | 4 |
| `POST /backend-anon/conversation` | 4 |
| `GET /backend-api/models?history_and_training_disabled=false` | 8.1 |
| `GET /backend-anon/models?iim=false&is_gizmo=false` | 8.1 |
| `GET /backend-api/me` | 8.2、11.4 |
| `POST /backend-api/conversation/init` | 8.2 |
| `GET /backend-api/accounts/check/v4-2023-04-27?timezone_offset_min=-480` | 8.2 |
| `POST /backend-api/files` | 5 |
| `PUT <upload_url>` | 5.2 |
| `POST /backend-api/files/<file_id>/uploaded` | 5.3 |
| `POST /backend-api/f/conversation/prepare` | 6.2、9.1、10.3 |
| `POST /backend-api/f/conversation` | 6.3、9.2、10.4 |
| `GET /backend-api/conversation/<conversation_id>` | 6.5、9.2、10.5 |
| `GET /backend-api/conversations?offset=0&limit=<n>&order=updated&conversation_filter=all` | 7.3 |
| `GET /backend-api/tasks` | 6.5 |
| `GET /backend-api/files/<file_id>/download` | 6.6、10.6 |
| `GET /backend-api/conversation/<conversation_id>/attachment/<attachment_id>/download` | 6.6、10.6 |
| `GET /backend-api/conversation/<conversation_id>/interpreter/download?message_id=...&sandbox_path=...` | 10.6 |
| `GET /backend-api/files/download/<file_id>?post_id=&inline=false` | 10.6 |
| `GET https://auth.openai.com/api/accounts/authorize?...` | 11.1、11.4、13 |
| `POST https://auth.openai.com/api/accounts/oauth/token` | 11.2、13 |
| `POST https://auth.openai.com/oauth/token` | 11.3 |
| `POST https://auth.openai.com/api/accounts/password/verify` | 11.4 |
| `POST https://sentinel.openai.com/backend-api/sentinel/req` | 12 |
| `POST https://auth.openai.com/api/accounts/user/register` | 13 |
| `GET https://auth.openai.com/api/accounts/email-otp/send` | 13 |
| `POST https://auth.openai.com/api/accounts/email-otp/validate` | 13 |
| `POST https://auth.openai.com/api/accounts/create_account` | 13 |

## 1. 逆向边界

### 1.1 上游域名

| 域名 | 用途 |
|:--|:--|
| `https://chatgpt.com` | ChatGPT 网页后端，包含模型列表、用户信息、对话、图片生成、文件上传、附件下载、任务状态等接口。 |
| `https://auth.openai.com` | OpenAI 账号 OAuth / 登录 / 注册 / token 兑换和刷新接口。 |
| `https://sentinel.openai.com` | 登录、注册等账号流程中的 Sentinel token 挑战接口。 |
| Azure Blob `upload_url` | `/backend-api/files` 返回的一次性上传地址，用于 PUT 原始文件内容。 |
| 下载签名 URL | `/backend-api/files/*/download` 或 conversation attachment download 返回的一次性实际资源下载地址。 |

### 1.2 已登录与匿名链路

`OpenAIBackendAPI(access_token="")` 走匿名链路；传入 `access_token` 走登录链路。

| 能力 | 登录链路 | 匿名链路 |
|:--|:--|:--|
| Chat requirements | `/backend-api/sentinel/chat-requirements` | `/backend-anon/sentinel/chat-requirements` |
| Conversation SSE | `/backend-api/conversation` | `/backend-anon/conversation` |
| Models | `/backend-api/models?history_and_training_disabled=false` | `/backend-anon/models?iim=false&is_gizmo=false` |
| 图片输入、图片生成、文件上传、搜索、可编辑文件 | 需要登录 | 不支持 |

匿名 conversation 的 timezone 使用 `America/Los_Angeles`；登录 conversation 使用 `Asia/Shanghai`。

## 2. 浏览器指纹与基础 Header

### 2.1 Session 初始化

核心实现位于 `services/openai_backend_api.py` 的 `OpenAIBackendAPI.__init__` 和 `_build_fp()`。

默认指纹：

| 字段 | 默认值或来源 |
|:--|:--|
| `User-Agent` | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) ... Edg/143.0.0.0` |
| `impersonate` | `edge101` |
| `OAI-Device-Id` | 账号存储里的 `oai-device-id`，否则新 UUID |
| `OAI-Session-Id` | 账号存储里的 `oai-session-id`，否则新 UUID |
| `Sec-Ch-Ua` | Edge/Chromium 143 形态 |
| `Sec-Ch-Ua-Mobile` | `?0` |
| `Sec-Ch-Ua-Platform` | `"Windows"` |

全局 session header 会设置：

```http
User-Agent: <browser UA>
Origin: https://chatgpt.com
Referer: https://chatgpt.com/
Accept-Language: zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7
Cache-Control: no-cache
Pragma: no-cache
Priority: u=1, i
Sec-Ch-Ua: <fp sec-ch-ua>
Sec-Ch-Ua-Arch: "x86"
Sec-Ch-Ua-Bitness: "64"
Sec-Ch-Ua-Full-Version: "143.0.3650.96"
Sec-Ch-Ua-Full-Version-List: "Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"
Sec-Ch-Ua-Mobile: ?0
Sec-Ch-Ua-Model: ""
Sec-Ch-Ua-Platform: "Windows"
Sec-Ch-Ua-Platform-Version: "19.0.0"
Sec-Fetch-Dest: empty
Sec-Fetch-Mode: cors
Sec-Fetch-Site: same-origin
OAI-Device-Id: <uuid>
OAI-Session-Id: <uuid>
OAI-Language: zh-CN
OAI-Client-Version: prod-a194cd50d4416d3c0b47c740f206b12ce60f5887
OAI-Client-Build-Number: 6708908
Authorization: Bearer <access_token>   # 仅登录链路
```

所有 ChatGPT 后端请求还会通过 `_headers(path, extra)` 加：

```http
X-OpenAI-Target-Path: <path>
X-OpenAI-Target-Route: <path or route-template>
```

部分 conversation 文档、下载接口会把 `X-OpenAI-Target-Route` 改成模板路径，例如 `/backend-api/conversation/{conversation_id}`。

### 2.2 首页 bootstrap

调用任何 conversation / models 前，先 GET `https://chatgpt.com/` 预热。请求头使用 document navigation 形态：

```http
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8
Sec-Fetch-Dest: document
Sec-Fetch-Mode: navigate
Sec-Fetch-Site: none
Sec-Fetch-User: ?1
Upgrade-Insecure-Requests: 1
```

返回 HTML 用于提取 PoW 资源：

1. 解析所有 `<script src="...">`。
2. 从脚本路径匹配 `c/[^/]*/_` 作为 `data_build`。
3. 如果脚本未携带 build，则从 `<html data-build="...">` 读取。
4. 没有脚本时 fallback 为 `https://chatgpt.com/backend-api/sentinel/sdk.js`。

这些资源会参与后续 `chat-requirements` 的 PoW token 构造。

## 3. Chat Requirements 与 PoW

### 3.1 获取 requirements

登录链路：

```http
POST https://chatgpt.com/backend-api/sentinel/chat-requirements
Content-Type: application/json
Authorization: Bearer <access_token>
X-OpenAI-Target-Path: /backend-api/sentinel/chat-requirements
X-OpenAI-Target-Route: /backend-api/sentinel/chat-requirements
```

匿名链路：

```http
POST https://chatgpt.com/backend-anon/sentinel/chat-requirements
Content-Type: application/json
X-OpenAI-Target-Path: /backend-anon/sentinel/chat-requirements
X-OpenAI-Target-Route: /backend-anon/sentinel/chat-requirements
```

请求体：

```json
{
  "p": "<legacy_requirements_token>"
}
```

`p` 由 `build_legacy_requirements_token(user_agent, script_sources, data_build)` 生成，固定前缀是 `gAAAAAC`。内部使用随机 seed、浏览器环境数组、`sha3_512(seed + base64(config))` 搜索满足难度 `0fffff` 的答案；未命中时返回 fallback 字符串。

### 3.2 requirements 响应处理

响应中关键字段：

| 字段 | 处理 |
|:--|:--|
| `token` | 后续 conversation 请求必须放入 `OpenAI-Sentinel-Chat-Requirements-Token`。 |
| `so_token` | 如存在，放入 `OpenAI-Sentinel-SO-Token`。 |
| `proofofwork.required` | 为 true 时，根据 `seed`、`difficulty` 生成 `OpenAI-Sentinel-Proof-Token`。 |
| `turnstile.required` 且有 `dx` | 调用 Turnstile 求解器，成功后放入 `OpenAI-Sentinel-Turnstile-Token`。 |
| `arkose.required` | 当前实现直接报错，不支持 Arkose。 |

Proof token 由 `build_proof_token()` 生成，固定前缀是 `gAAAAAB`，同样使用网页脚本来源、`data_build` 和浏览器环境数组参与计算。

### 3.3 Turnstile 求解实现

当 `turnstile.required == true` 且响应中有 `turnstile.dx` 时，调用 `solve_turnstile_token(dx, source_p)`。登录链路传空字符串作为 `source_p`；匿名链路传本次 requirements 请求体里的 `p`。

实现步骤：

1. base64 解码 `dx`。
2. 使用 `source_p` 对解码后的字符串做循环 XOR。
3. JSON 解析得到一个指令列表。
4. 用一个 `process_map` 模拟有限的浏览器运行环境，支持 `window.Math`、`window.Reflect`、`window.performance`、`window.localStorage`、`window.Object.*` 等字符串化结果。
5. 逐条执行指令，遇到输出指令时把结果 base64 编码。
6. 成功时返回该 base64 字符串，作为 `OpenAI-Sentinel-Turnstile-Token`。

该实现不是调用外部验证码服务，而是按上游下发的 `dx` 指令解释执行。解释器遇到单条指令异常会跳过该指令，最后没有生成结果则返回空。

## 4. 普通 Conversation SSE

### 4.1 入口

登录文本对话：

```http
POST https://chatgpt.com/backend-api/conversation
Accept: text/event-stream
Content-Type: application/json
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
OpenAI-Sentinel-Proof-Token: <optional>
OpenAI-Sentinel-Turnstile-Token: <optional>
OpenAI-Sentinel-SO-Token: <optional>
Authorization: Bearer <access_token>
```

匿名文本对话：

```http
POST https://chatgpt.com/backend-anon/conversation
Accept: text/event-stream
Content-Type: application/json
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
```

### 4.2 请求体

`_conversation_payload(messages, model, timezone)` 构造：

```json
{
  "action": "next",
  "messages": [
    {
      "id": "<uuid>",
      "author": {"role": "user"},
      "content": {"content_type": "text", "parts": ["hello"]}
    }
  ],
  "model": "auto",
  "parent_message_id": "<uuid>",
  "conversation_mode": {"kind": "primary_assistant"},
  "conversation_origin": null,
  "force_paragen": false,
  "force_paragen_model_slug": "",
  "force_rate_limit": false,
  "force_use_sse": true,
  "history_and_training_disabled": true,
  "reset_rate_limits": false,
  "suggestions": [],
  "supported_encodings": [],
  "system_hints": [],
  "timezone": "Asia/Shanghai",
  "timezone_offset_min": -480,
  "variant_purpose": "comparison_implicit",
  "websocket_request_id": "<uuid>",
  "client_contextual_info": {
    "is_dark_mode": false,
    "time_since_loaded": 120,
    "page_height": 900,
    "page_width": 1400,
    "pixel_ratio": 2,
    "screen_height": 1440,
    "screen_width": 2560
  }
}
```

文本 message 的 `content` 支持：

| 输入形态 | 网页 conversation 形态 |
|:--|:--|
| 字符串 | `{"content_type":"text","parts":[text]}` |
| 含图片的 list | 先上传图片，再构造 `{"content_type":"multimodal_text","parts":[image_asset_pointer..., text]}` |

多模态普通对话中的图片输入必须登录。图片上传后作为：

```json
{
  "content_type": "image_asset_pointer",
  "asset_pointer": "file-service://<file_id>",
  "width": 123,
  "height": 456,
  "size_bytes": 7890
}
```

同时 message metadata 添加 `attachments`：

```json
{
  "attachments": [
    {
      "id": "<file_id>",
      "mimeType": "image/png",
      "name": "image_1.png",
      "size": 7890,
      "width": 123,
      "height": 456
    }
  ]
}
```

### 4.3 SSE 读取规则

只读取以 `data:` 开头的行：

```text
data: {"type":"resume_conversation_token",...}
data: {"p":"/message/content/parts/0","o":"append","v":"hello"}
data: [DONE]
```

空行、非 `data:` 行忽略。`[DONE]` 表示流结束。

### 4.4 SSE 状态机

客户端维护：

| 状态 | 来源 |
|:--|:--|
| `conversation_id` | 任意 payload 中的 `"conversation_id"` 字段，或事件顶层 / `v.conversation_id`。 |
| `raw_text` / `text` | assistant message 全量文本或 patch 增量。 |
| `file_ids` | 图片工具输出中的 `file-service://...` 或真实图片 `file_00000000 + 24 hex`。 |
| `sediment_ids` | 图片工具输出中的 `sediment://...`。 |
| `blocked` | `type=moderation` 且 `moderation_response.blocked == true`。 |
| `tool_invoked` | `type=server_ste_metadata` 的 `metadata.tool_invoked`。 |
| `turn_use_case` | `type=server_ste_metadata` 的 `metadata.turn_use_case`。 |

文本 patch 处理：

| 事件形态 | 行为 |
|:--|:--|
| `p == "/message/content/parts/0"` 且 `o == "append"` | 追加 `v`。 |
| `p == "/message/content/parts/0"` 且 `o == "replace"` | 用 `v` 替换文本。 |
| `o == "patch"` 且 `v` 是数组 | 按数组顺序递归处理 patch。 |
| 顶层 `v` 是字符串且没有 `p/o` | 视为当前文本追加。 |
| `v.message.author.role == "assistant"` | 可从完整 message 中读取全量文本。 |

输出文本会清理 ChatGPT 网页富注释私有字符：`\ue200...\ue201`。URL 注释保留可读 label 和 URL，citation / source 内部指针会被移除或压缩为可读标签。

## 5. 文件上传

### 5.1 创建文件记录

图片输入或图片编辑参考图先 POST：

```http
POST https://chatgpt.com/backend-api/files
Content-Type: application/json
Accept: application/json
Authorization: Bearer <access_token>
```

普通多模态和图片编辑请求体：

```json
{
  "file_name": "image_1.png",
  "file_size": 12345,
  "use_case": "multimodal",
  "width": 1024,
  "height": 768
}
```

可编辑文件链路使用的上传体略有差异：

```json
{
  "file_name": "image_1.png",
  "file_size": 12345,
  "use_case": "multimodal",
  "timezone_offset_min": -480,
  "reset_rate_limits": false,
  "store_in_library": true,
  "library_persistence_mode": "opportunistic"
}
```

响应必须包含：

| 字段 | 用途 |
|:--|:--|
| `file_id` | 后续 conversation 中的文件 ID。 |
| `upload_url` | 一次性 Azure Blob PUT URL。 |
| `library_file_id` | 可编辑文件链路的 library 附件引用。 |

### 5.2 上传原始文件内容

对 `upload_url` 执行 PUT：

```http
PUT <upload_url>
Content-Type: <image mime>
x-ms-blob-type: BlockBlob
x-ms-version: 2020-04-08
Origin: https://chatgpt.com
Referer: https://chatgpt.com/
User-Agent: <browser UA>
Accept: application/json, text/plain, */*
```

body 是原始图片 bytes。

### 5.3 标记上传完成

```http
POST https://chatgpt.com/backend-api/files/<file_id>/uploaded
Content-Type: application/json
Accept: application/json
```

body 为 `{}`。成功后，文件可在 conversation message 的 `asset_pointer` 或 `attachments` 中使用。

### 5.4 图片输入解码与远程图片获取

网页逆向调用前，项目会把多种下游图片表达统一成 `(bytes, mime)`，再走第 5 节上传。

支持的 message content 图片形态：

| 输入类型 | 字段 | 处理 |
|:--|:--|:--|
| `image_url` | `image_url` / `url` / 对象本身 | 支持 data URL 和 HTTP(S) URL。 |
| `input_image` | `image_url` / `url` / `b64_json` / `base64` / `source` | 解码为 bytes。 |
| `image` | `data` bytes、URL、base64 | 解码为 bytes。 |

远程图片获取细节：

```http
GET <image_url>
Accept: image/*,*/*;q=0.8
User-Agent: chatgpt2api vision fetcher
```

限制和校验：

| 项 | 规则 |
|:--|:--|
| URL scheme | 仅 `http` / `https`。 |
| 超时 | 20 秒。 |
| 重定向 | 允许。 |
| 大小限制 | `Content-Length` 或实际响应体超过 10MB 均拒绝。 |
| MIME | `Content-Type` 必须是 `image/*`，或是 `application/octet-stream` / `binary/octet-stream` 且可从 URL path 猜到图片 MIME。 |
| 空响应 | 拒绝。 |

data URL 形态从 `data:<mime>;base64,<payload>` 中取 MIME 和 base64；缺省 MIME 为 `image/png`。`image/jpg` 会归一为 `image/jpeg`。

`_upload_image()` 还支持本地路径输入：当图片字符串长度小于 512、不是 data URL、不含换行，并且展开 `~` 后是存在的本地文件时，直接读取该文件 bytes，并使用本地文件名作为上传文件名。否则按 base64 字符串解码；data URL 会先去掉逗号前的 header。

## 6. 网页图片生成与编辑

### 6.1 前置条件

网页图片链路必须登录。实现中用于图片的 `system_hints` 是 `["picture_v2"]`。模型映射：

| 对外或调用侧模型 | 网页 model slug |
|:--|:--|
| `gpt-image-2` | `gpt-5-3` |
| 其他未知图片模型 | `auto` |

如请求包含参考图，先按第 5 节上传每张图片。

### 6.2 prepare conversation

先调用：

```http
POST https://chatgpt.com/backend-api/f/conversation/prepare
Content-Type: application/json
Accept: */*
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
OpenAI-Sentinel-Proof-Token: <optional>
Authorization: Bearer <access_token>
```

请求体：

```json
{
  "action": "next",
  "fork_from_shared_post": false,
  "parent_message_id": "<uuid>",
  "model": "gpt-5-3",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "system_hints": ["picture_v2"],
  "partial_query": {
    "id": "<uuid>",
    "author": {"role": "user"},
    "content": {"content_type": "text", "parts": ["<prompt>"]}
  },
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {"app_name": "chatgpt.com"}
}
```

响应读取 `conduit_token`。

### 6.3 启动图片 SSE

```http
POST https://chatgpt.com/backend-api/f/conversation
Accept: text/event-stream
Content-Type: application/json
X-Conduit-Token: <conduit_token>
X-Oai-Turn-Trace-Id: <uuid>
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
OpenAI-Sentinel-Proof-Token: <optional>
Authorization: Bearer <access_token>
```

无参考图时，message content：

```json
{
  "content_type": "text",
  "parts": ["<prompt>"]
}
```

有参考图时，message content：

```json
{
  "content_type": "multimodal_text",
  "parts": [
    {
      "content_type": "image_asset_pointer",
      "asset_pointer": "file-service://<input_file_id>",
      "width": 1024,
      "height": 768,
      "size_bytes": 12345
    },
    "<prompt>"
  ]
}
```

完整请求体关键字段：

```json
{
  "action": "next",
  "messages": [
    {
      "id": "<uuid>",
      "author": {"role": "user"},
      "create_time": 1710000000.0,
      "content": "<见上>",
      "metadata": {
        "developer_mode_connector_ids": [],
        "selected_github_repos": [],
        "selected_all_github_repos": false,
        "system_hints": ["picture_v2"],
        "serialization_metadata": {"custom_symbol_offsets": []},
        "attachments": [
          {
            "id": "<input_file_id>",
            "mimeType": "image/png",
            "name": "image_1.png",
            "size": 12345,
            "width": 1024,
            "height": 768
          }
        ]
      }
    }
  ],
  "parent_message_id": "<uuid>",
  "model": "gpt-5-3",
  "client_prepare_state": "sent",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "enable_message_followups": true,
  "system_hints": ["picture_v2"],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {
    "is_dark_mode": false,
    "time_since_loaded": 1200,
    "page_height": 1072,
    "page_width": 1724,
    "pixel_ratio": 1.2,
    "screen_height": 1440,
    "screen_width": 2560,
    "app_name": "chatgpt.com"
  },
  "paragen_cot_summary_display_override": "allow",
  "force_parallel_switch": "auto"
}
```

### 6.4 图片输出识别

图片输出不能只靠字符串匹配。实现的严格条件：

1. payload 中发现 `conversation_id` 后保存。
2. 只有处于图片上下文时才收集 file id：
   - 事件是图片工具消息：`message.author.role == "tool"` 且 `metadata.async_task_type == "image_gen"`；或
   - 已看到 `server_ste_metadata.metadata.tool_invoked == true`，且当前事件不是用户消息；或
   - patch payload 包含 `asset_pointer` / `file-service://`，且当前事件不是用户消息。
3. `file_ids` 来源：
   - `file-service://<id>`；
   - 或真实图片文件 ID 正则：`file_00000000[a-f0-9]{24}`。
4. `sediment_ids` 来源：`sediment://<id>`。
5. 用户输入消息里的 `file-service://...` 或 `sediment://...` 是输入附件，不是输出图片。
6. `file_upload` 等占位值必须忽略。

图片工具成功消息常见形态：

```json
{
  "v": {
    "message": {
      "author": {"role": "tool"},
      "content": {
        "content_type": "multimodal_text",
        "parts": [
          {"asset_pointer": "file-service://file_00000000..."},
          {"asset_pointer": "sediment://file_00000000..."}
        ]
      },
      "metadata": {"async_task_type": "image_gen"}
    },
    "conversation_id": "<conversation_id>"
  }
}
```

### 6.5 SSE 结束后的图片轮询

SSE 结束后，如果已有 `conversation_id`，会继续查询完整 conversation 文档，直到找到图片工具输出或超时。

```http
GET https://chatgpt.com/backend-api/conversation/<conversation_id>
Accept: application/json
X-OpenAI-Target-Route: /backend-api/conversation/{conversation_id}
Referer: https://chatgpt.com/c/<conversation_id>
Authorization: Bearer <access_token>
```

轮询逻辑：

| 条件 | 行为 |
|:--|:--|
| 初始 SSE 已有 file/sediment id | 可先 settle 一段时间再二次确认。 |
| conversation 文档暂时 429 / 5xx | 按 `Retry-After` 或指数退避重试。 |
| 找到 file/sediment id | 视配置决定是否立即返回，或等待后再次确认。 |
| 未找到图片但 assistant 文本含内容策略拒绝关键词 | 抛出内容策略错误。 |
| 超时仍无图片 | 抛出图片轮询超时错误。 |

项目还会查询任务接口辅助识别错误：

```http
GET https://chatgpt.com/backend-api/tasks
Accept: application/json
Authorization: Bearer <access_token>
```

返回 `tasks` 后按 `conversation_id` 或 `original_conversation_id` 过滤。结构化错误判断：

| 字段 | 条件 |
|:--|:--|
| `image_gen_message.metadata.is_error` | `true` |
| `image_gen_message.author.role` | `assistant` |
| `image_gen_message.content.content_type` | `text` |

错误文本从 `image_gen_message.content.parts` 拼接。

### 6.6 图片下载 URL 解析

`file_ids` 走：

```http
GET https://chatgpt.com/backend-api/files/<file_id>/download
Accept: application/json
Authorization: Bearer <access_token>
```

`sediment_ids` 走：

```http
GET https://chatgpt.com/backend-api/conversation/<conversation_id>/attachment/<attachment_id>/download
Accept: application/json
Authorization: Bearer <access_token>
```

两者响应均读取 `download_url` 或 `url`。随后 GET 该签名 URL 下载图片 bytes。

下载图片 bytes 时使用同一个 `curl_cffi` session 直接 GET 签名 URL，超时 120 秒。若多个 URL 返回完全相同的 bytes，结果会去重。

## 7. 内容拒绝与异常判断

### 7.1 Moderation 事件

```json
{
  "type": "moderation",
  "moderation_response": {"blocked": true},
  "conversation_id": "<conversation_id>"
}
```

`blocked == true` 表示被策略拦截。若后续 assistant 有文本，优先返回该文本；若没有文本，可查询 tasks 获取结构化错误。

conversation 文档轮询时还会扫描 assistant/tool 文本中的内容策略关键词。关键词包括：

| 类别 | 关键词 |
|:--|:--|
| 明确策略 | `内容政策`、`防护限制`、`违反`、`moderation`、`policy`、`blocked` |
| 拒绝生成 | `不能生成`、`无法生成`、`不能帮助`、`无法帮助` |
| 敏感内容 | `裸体`、`裸露`、`色情`、`性内容`、`未成年` |
| 通用拒绝 | `抱歉，我不能` |

匹配时会把错误文本转成 lowercase，因此英文关键词大小写不敏感。

### 7.2 server_ste_metadata

```json
{
  "type": "server_ste_metadata",
  "metadata": {
    "tool_invoked": false,
    "turn_use_case": "multimodal",
    "did_prompt_contain_image": true
  },
  "conversation_id": "<conversation_id>"
}
```

判断含义：

| 字段 | 含义 |
|:--|:--|
| `tool_invoked == true` | 上游认为本轮调用过工具，可能需要继续轮询结果。 |
| `tool_invoked == false` | 通常没有工具结果，不应从用户输入附件里提取输出图。 |
| `turn_use_case == "image gen"` | SSE 未给图片时仍应轮询 conversation 文档。 |
| `did_prompt_contain_image == true` | 只说明输入有图，不说明输出有图。 |

### 7.3 模型返回工具参数文本

有时上游没有在 SSE 中直接产生工具消息，而是返回包含工具参数的文本，例如带 `referenced_image_ids` 或 `{"size":"1920x1088","n":1}`。实现把这类情况视为“图片可能在异步生成”，而不是普通文本结果：

1. 若缺少 `conversation_id`，尝试通过最近对话列表恢复。
2. 使用更长轮询超时，继续查询 conversation 文档。
3. 只有多次轮询仍无图片时，才返回文本或错误消息。

最近对话恢复接口：

```http
GET https://chatgpt.com/backend-api/conversations?offset=0&limit=10&order=updated&conversation_filter=all
Accept: application/json
Authorization: Bearer <access_token>
```

匹配依据包括更新时间、标题与 prompt 词汇重合度，以及图片请求常见 `Image...` 标题。

### 7.4 HTTP 错误结构

所有上游 HTTP 响应统一通过 `ensure_ok(response, context)` 检查。非 2xx 时抛出结构化 `UpstreamHTTPError`：

| 字段 | 含义 |
|:--|:--|
| `context` | 当前调用上下文或 path。 |
| `status_code` | 上游 HTTP 状态码。 |
| `body` | JSON body；如果不是 JSON，则保存 response text。 |
| `retry_after` | 若响应头 `Retry-After` 是整数，则保存为 int。 |

错误消息里的 body 只截断展示，实例上保留完整 body。图片轮询会对 429 和 5xx 使用 `retry_after` 或指数退避；401 在用户信息路径中会转成 token invalid 语义。

## 8. 模型与账号信息

### 8.1 模型列表

登录：

```http
GET https://chatgpt.com/backend-api/models?history_and_training_disabled=false
X-OpenAI-Target-Path: /backend-api/models
X-OpenAI-Target-Route: /backend-api/models
Authorization: Bearer <access_token>
```

匿名：

```http
GET https://chatgpt.com/backend-anon/models?iim=false&is_gizmo=false
X-OpenAI-Target-Path: /backend-anon/models
X-OpenAI-Target-Route: /backend-anon/models
```

响应读取 `models[]`，每个模型使用：

| 字段 | 用途 |
|:--|:--|
| `slug` | 模型 ID。 |
| `created` | 创建时间。 |
| `owned_by` | 所属方，缺省为 `chatgpt`。 |

### 8.2 当前用户与额度

登录账号信息并行请求三个接口：

```http
GET https://chatgpt.com/backend-api/me
Authorization: Bearer <access_token>
```

```http
POST https://chatgpt.com/backend-api/conversation/init
Content-Type: application/json
Authorization: Bearer <access_token>
```

`conversation/init` 请求体：

```json
{
  "gizmo_id": null,
  "requested_default_model": null,
  "conversation_id": null,
  "timezone_offset_min": -480
}
```

```http
GET https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27?timezone_offset_min=-480
Authorization: Bearer <access_token>
```

信息来源：

| 信息 | 来源 |
|:--|:--|
| email / user id | `/backend-api/me` |
| default model / limits_progress | `/backend-api/conversation/init` |
| plan type / account id / entitlement | `/backend-api/accounts/check/v4-2023-04-27` |
| 图片额度 | `limits_progress[]` 中 `feature_name == "image_gen"` 的 `remaining` |
| 图片额度恢复时间 | 同一项的 `reset_after` |

若 `limits_progress` 里没有 `image_gen`，实现标记为 `image_quota_unknown=true`。

账号类型归一规则：

| 输入 | 归一结果 |
|:--|:--|
| `free` | `free` |
| `plus` | `Plus` |
| `pro` | `Pro` |
| `prolite` / `pro_lite` | `ProLite` |
| `team` / `business` | `Team` |
| `enterprise` | `Enterprise` |

`source_type` 缺省归一为 `web`。账号状态为 `禁用`、`限流`、`异常` 时不可作为图片账号；如果 `image_quota_unknown=true`，即使 quota 为 0 也允许视为可用。

## 9. 搜索链路

网页搜索链路需要登录，核心是 `system_hints=["search"]`、`force_use_search=true`。

### 9.1 prepare

```http
POST https://chatgpt.com/backend-api/f/conversation/prepare
Accept: */*
Content-Type: application/json
X-Conduit-Token: no-token
Authorization: Bearer <access_token>
```

请求体关键字段：

```json
{
  "action": "next",
  "fork_from_shared_post": false,
  "parent_message_id": "client-created-root",
  "model": "gpt-5-5",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "system_hints": ["search"],
  "partial_query": {
    "id": "<uuid>",
    "author": {"role": "user"},
    "content": {"content_type": "text", "parts": ["<prompt>"]}
  },
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {"app_name": "chatgpt.com"}
}
```

响应读取 `conduit_token`。

### 9.2 启动搜索 conversation

```http
POST https://chatgpt.com/backend-api/f/conversation
Accept: text/event-stream
Content-Type: application/json
X-Conduit-Token: <conduit_token>
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
Authorization: Bearer <access_token>
```

请求体关键字段：

```json
{
  "action": "next",
  "messages": [
    {
      "id": "<uuid>",
      "author": {"role": "user"},
      "create_time": 1710000000.0,
      "content": {"content_type": "text", "parts": ["<prompt>"]},
      "metadata": {
        "developer_mode_connector_ids": [],
        "selected_github_repos": [],
        "selected_all_github_repos": false,
        "system_hints": ["search"],
        "serialization_metadata": {"custom_symbol_offsets": []}
      }
    }
  ],
  "parent_message_id": "client-created-root",
  "model": "gpt-5-5",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "enable_message_followups": true,
  "system_hints": [],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "force_use_search": true,
  "client_reported_search_source": "conversation_composer_web_icon",
  "client_contextual_info": {
    "is_dark_mode": false,
    "time_since_loaded": 36,
    "page_height": 925,
    "page_width": 886,
    "pixel_ratio": 2,
    "screen_height": 1440,
    "screen_width": 2560,
    "app_name": "chatgpt.com"
  },
  "paragen_cot_summary_display_override": "allow",
  "force_parallel_switch": "auto"
}
```

SSE 中只提取 `conversation_id`。随后轮询 conversation 文档，取最新 assistant message：

| 字段 | 提取方式 |
|:--|:--|
| `answer` | assistant message content 中的 `text`、字符串 parts 或 dict part 的 `text/summary/content`。 |
| `status` | `metadata.finish_details.type`，其次 `metadata.status` 或递归查找 `status`。 |
| `sources` | 递归扫描 message 中含 `url/link/source_url/metadata.url` 的对象。 |
| 兜底 URL | 从 answer 文本中正则提取 `http(s)://...`。 |

搜索完成状态包括 `finished_successfully` 和 `finished_partial_completion`。若 answer 连续稳定多次，也会提前返回。

## 10. 可编辑文件导出链路

此链路用于让网页端生成 PPT / PSD / ZIP 等可下载附件。它属于 ChatGPT 网页 conversation 和附件下载逆向，不属于前端业务逻辑。

### 10.1 参数特征

| 字段 | 值 |
|:--|:--|
| model | `gpt-5-5-thinking` |
| thinking effort | `extended` |
| client version | `prod-bede35f9dcd856d080e012478f0c1031faa2588e` |
| client build number | `6631702` |
| parent message id | `client-created-root` |
| prepare `X-Conduit-Token` | `no-token` |

进入该链路前，session header 的 `OAI-Client-Version` 和 `OAI-Client-Build-Number` 会改成上表值。

### 10.2 上传参考图片

参考图按第 5 节上传，但创建文件记录时启用 library：

```json
{
  "file_name": "image_1.png",
  "file_size": 12345,
  "use_case": "multimodal",
  "timezone_offset_min": -480,
  "reset_rate_limits": false,
  "store_in_library": true,
  "library_persistence_mode": "opportunistic"
}
```

message 中图片指针使用 `sediment://<file_id>`，metadata attachment 包含：

```json
{
  "id": "<file_id>",
  "size": 12345,
  "name": "image_1.png",
  "mime_type": "image/png",
  "width": 1024,
  "height": 768,
  "source": "library",
  "library_file_id": "<library_file_id>",
  "is_big_paste": false
}
```

### 10.3 prepare

```http
POST https://chatgpt.com/backend-api/f/conversation/prepare
Accept: */*
Content-Type: application/json
X-Conduit-Token: no-token
Authorization: Bearer <access_token>
```

请求体：

```json
{
  "action": "next",
  "fork_from_shared_post": false,
  "parent_message_id": "client-created-root",
  "model": "gpt-5-5-thinking",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "system_hints": [],
  "partial_query": {
    "id": "<uuid>",
    "author": {"role": "user"},
    "content": {"content_type": "text", "parts": ["<prompt>"]}
  },
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {"app_name": "chatgpt.com"},
  "thinking_effort": "extended",
  "attachment_mime_types": ["image/png"]
}
```

响应读取 `conduit_token`。

### 10.4 运行 conversation

```http
POST https://chatgpt.com/backend-api/f/conversation
Accept: text/event-stream
Content-Type: application/json
X-Conduit-Token: <conduit_token>
OpenAI-Sentinel-Chat-Requirements-Token: <requirements.token>
Authorization: Bearer <access_token>
```

请求体关键字段：

```json
{
  "action": "next",
  "messages": [
    {
      "id": "<uuid>",
      "author": {"role": "user"},
      "create_time": 1710000000.0,
      "content": {
        "content_type": "multimodal_text",
        "parts": [
          {
            "content_type": "image_asset_pointer",
            "asset_pointer": "sediment://<file_id>",
            "size_bytes": 12345,
            "width": 1024,
            "height": 768
          },
          "<prompt>"
        ]
      },
      "metadata": {
        "attachments": ["<见 10.2>"],
        "developer_mode_connector_ids": [],
        "selected_sources": [],
        "selected_github_repos": [],
        "selected_all_github_repos": false,
        "serialization_metadata": {"custom_symbol_offsets": []}
      }
    }
  ],
  "parent_message_id": "client-created-root",
  "model": "gpt-5-5-thinking",
  "client_prepare_state": "sent",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": {"kind": "primary_assistant"},
  "enable_message_followups": true,
  "system_hints": [],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {
    "is_dark_mode": false,
    "time_since_loaded": 401,
    "page_height": 1138,
    "page_width": 803,
    "pixel_ratio": 2,
    "screen_height": 1440,
    "screen_width": 2560,
    "app_name": "chatgpt.com"
  },
  "paragen_cot_summary_display_override": "allow",
  "force_parallel_switch": "auto",
  "thinking_effort": "extended"
}
```

SSE 阶段只要求提取 `conversation_id`。

### 10.5 提取可下载附件

轮询：

```http
GET https://chatgpt.com/backend-api/conversation/<conversation_id>
Accept: */*
Referer: https://chatgpt.com/c/<conversation_id>
X-OpenAI-Target-Route: /backend-api/conversation/{conversation_id}
Authorization: Bearer <access_token>
```

从 `mapping` 中按 `message.create_time` 排序，只检查 `author.role in {"assistant","tool"}` 的消息。附件来源：

1. `message.metadata.attachments[]`。
2. message 任意嵌套 dict 中含 `id/file_id/asset_pointer/name/file_name/filename/mime_type/mimeType` 的对象。
3. assistant 文本中匹配 `sandbox:/mnt/data/...` 或 `/mnt/data/...` 的导出路径。

文件 ID 识别：

| 来源 | 正则 |
|:--|:--|
| asset pointer | `(file-service|sediment)://([A-Za-z0-9_-]+)` |
| 普通 file id | `\b(file[-_](?!service\b)[A-Za-z0-9_-]+)\b` |

目标文件判断：

| 类型 | 判断 |
|:--|:--|
| PPT | 文件名或 sandbox path 以 `.ppt/.pptx` 结尾，或 MIME 包含 `presentationml.presentation` / `ms-powerpoint`。 |
| PSD | 文件名或 sandbox path 以 `.psd` 结尾，或 MIME 包含 `photoshop`。 |
| ZIP | 文件名或 path 以 `.zip` 结尾，或 MIME 是 `application/zip` / `application/x-zip-compressed` / 以 `/zip` 结尾。 |

### 10.6 下载附件

按优先级解析下载 URL：

1. sandbox path：

```http
GET https://chatgpt.com/backend-api/conversation/<conversation_id>/interpreter/download?message_id=<message_id>&sandbox_path=<sandbox_path>
Accept: */*
Referer: https://chatgpt.com/c/<conversation_id>
X-OpenAI-Target-Route: /backend-api/conversation/{conversation_id}/interpreter/download
Authorization: Bearer <access_token>
```

2. conversation attachment：

```http
GET https://chatgpt.com/backend-api/conversation/<conversation_id>/attachment/<attachment_id>/download
X-OpenAI-Target-Route: /backend-api/conversation/{conversation_id}/attachment/{attachment_id}/download
```

3. files download 新路径：

```http
GET https://chatgpt.com/backend-api/files/download/<file_id>?post_id=&inline=false
X-OpenAI-Target-Route: /backend-api/files/download/{file_id}
```

4. files download 旧路径：

```http
GET https://chatgpt.com/backend-api/files/<file_id>/download
X-OpenAI-Target-Route: /backend-api/files/download/{file_id}
```

以上接口响应读取 `download_url` 或 `url`，再 GET 该 URL 保存二进制文件。

## 11. OAuth / 登录相关网页逆向

### 11.1 Platform OAuth + PKCE

客户端信息：

| 字段 | 值 |
|:--|:--|
| auth base | `https://auth.openai.com` |
| platform base | `https://platform.openai.com` |
| client_id | `app_2SKx67EdpoN0G6j64rFvigXD` |
| audience | `https://api.openai.com/v1` |
| redirect_uri | `https://platform.openai.com/auth/callback` |
| scope | `openid profile email offline_access` |
| response_type | `code` |
| response_mode | `query` |
| code_challenge_method | `S256` |
| auth0Client | `eyJuYW1lIjoiYXV0aDAtc3BhLWpzIiwidmVyc2lvbiI6IjEuMjEuMCJ9` |

Authorize URL：

```http
GET https://auth.openai.com/api/accounts/authorize?<params>
```

参数：

```json
{
  "issuer": "https://auth.openai.com",
  "client_id": "app_2SKx67EdpoN0G6j64rFvigXD",
  "audience": "https://api.openai.com/v1",
  "redirect_uri": "https://platform.openai.com/auth/callback",
  "device_id": "<uuid>",
  "screen_hint": "login_or_signup",
  "max_age": "0",
  "login_hint": "<email optional>",
  "scope": "openid profile email offline_access",
  "response_type": "code",
  "response_mode": "query",
  "state": "<random>",
  "nonce": "<random>",
  "code_challenge": "<S256(code_verifier)>",
  "code_challenge_method": "S256",
  "auth0Client": "<auth0 client b64>"
}
```

成功后 callback URL 携带 `code` 和 `state`。

PKCE 生成方式：

1. `code_verifier = base64url(random 64 bytes)`，去掉尾部 `=`。
2. `code_challenge = base64url(sha256(code_verifier))`，去掉尾部 `=`。
3. `code_challenge_method` 固定 `S256`。

手动 OAuth 桥会维护一个 10 分钟 TTL 的临时 session：

| 字段 | 用途 |
|:--|:--|
| `session_id` | 本地会话 ID。 |
| `state` | 形如 `<session_id>.<nonce>`，用于从 callback URL 反查 verifier。 |
| `code_verifier` | 换 token 时必须和 authorization code 配对。 |
| `redirect_uri` | 默认 `https://platform.openai.com/auth/callback`。 |

`finish()` 支持用户粘贴完整 callback URL 或只粘贴 raw code。若 callback URL 中带 `state`，优先使用 `state` 里的 session id；同时校验 state 必须和本地保存值一致。只有成功换 token 后才删除临时 session，失败不会立即消耗本地 verifier。

### 11.2 authorization code 换 token

```http
POST https://auth.openai.com/api/accounts/oauth/token
Content-Type: application/json
Origin: https://platform.openai.com
Referer: https://platform.openai.com/
auth0-client: <auth0Client>
```

请求体：

```json
{
  "client_id": "app_2SKx67EdpoN0G6j64rFvigXD",
  "code_verifier": "<pkce code_verifier>",
  "grant_type": "authorization_code",
  "code": "<authorization code>",
  "redirect_uri": "https://platform.openai.com/auth/callback"
}
```

成功响应至少包含：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "id_token": "..."
}
```

`offline_access` 正常应返回 `refresh_token`。缺失时无法走 refresh_token 自动刷新。

### 11.3 refresh_token 刷新 access_token

```http
POST https://auth.openai.com/oauth/token
Content-Type: application/x-www-form-urlencoded
Accept: application/json
User-Agent: <browser UA>
```

form body：

```text
grant_type=refresh_token
refresh_token=<refresh_token>
client_id=app_2SKx67EdpoN0G6j64rFvigXD
```

成功响应：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "id_token": "..."
}
```

若响应没有新的 `refresh_token`，实现保留旧 refresh token。若错误文本包含 `app_session_terminated`，项目会尝试密码重新登录；该行为属于账号维护逻辑，不是 conversation 逆向必要步骤。

刷新调度参数：

| 参数 | 值 | 含义 |
|:--|:--|:--|
| access token refresh skew | 24 小时 | access token 距过期小于等于 24 小时时需要刷新。 |
| token refresh error backoff | 5 分钟 | 非强制刷新时，最近 5 分钟有刷新错误则跳过。 |
| refresh token keepalive interval | 3 天 | 按锚点周期性强制刷新以保活 refresh token。 |
| refresh token keepalive error backoff | 6 小时 | keepalive 最近失败后 6 小时内不再尝试。 |
| keepalive batch size | 3 | 每轮最多保活 3 个账号。 |

JWT 解析采用 base64url 解码第二段 payload，自动补齐 `=`。过期时间读取 `exp`。

### 11.4 密码登录关键步骤

密码重新登录复用 Platform OAuth + PKCE：

1. 生成 `device_id`、`code_verifier`、`code_challenge`。
2. 设置 cookie `oai-did=<device_id>` 到 `.auth.openai.com` 和 `auth.openai.com`。
3. GET `/api/accounts/authorize?...`，allow redirects。
4. POST `/api/accounts/password/verify`：

```http
POST https://auth.openai.com/api/accounts/password/verify
Content-Type: application/json
Origin: https://auth.openai.com
Referer: https://auth.openai.com/email-verification
oai-device-id: <device_id>
openai-sentinel-token: <sentinel token>
```

请求体：

```json
{"password": "<password>"}
```

成功响应中 `continue_url` 携带 authorization code。若返回 `page.type == "email_otp_verification"`，说明需要邮箱验证码。

密码登录的错误识别：

| 条件 | 返回语义 |
|:--|:--|
| authorize 最终 URL 含 `/error?payload=` | base64 解码 payload，读取 `errorCode`。 |
| `errorCode == rate_limit_exceeded` | 识别为限流。 |
| `/api/accounts/password/verify` 返回 `unsupported_country_region_territory` | 识别为地区不支持。 |
| 返回 `invalid_state` | 识别为 OAuth state 异常。 |
| 错误文本含 `Invalid credentials` 或 `wrong password` | 识别为密码错误。 |
| `page.type == email_otp_verification` | 识别为需要邮箱验证码。 |

密码登录拿到 token 后还会请求 `https://chatgpt.com/backend-api/me`，并解析 access token JWT：

| 信息 | 来源 |
|:--|:--|
| email | JWT `https://api.openai.com/profile.email`，缺省使用输入 email。 |
| account id | JWT `https://api.openai.com/auth.chatgpt_account_id`，缺省使用 `/backend-api/me.account.account_id`。 |
| expires_at | JWT `exp`。 |

## 12. Sentinel 登录 token

账号登录、注册、OTP 校验等流程会调用独立 Sentinel：

```http
POST https://sentinel.openai.com/backend-api/sentinel/req
Content-Type: text/plain;charset=UTF-8
Referer: https://sentinel.openai.com/backend-api/sentinel/frame.html
Origin: https://sentinel.openai.com
User-Agent: <Chrome UA>
sec-ch-ua: <Chrome sec-ch-ua>
sec-ch-ua-mobile: ?0
sec-ch-ua-platform: "Windows"
```

请求 body 是 JSON 字符串：

```json
{
  "p": "<requirements token>",
  "id": "<device_id>",
  "flow": "password_verify"
}
```

常见 `flow`：

| flow | 场景 |
|:--|:--|
| `password_verify` | 密码登录验证。 |
| `authorize_continue` | 邮箱 OTP 校验后继续授权。 |
| `username_password_create` | 注册提交邮箱密码。 |
| `oauth_create_account` | 注册创建账号资料。 |

Sentinel 响应读取 `token` 和可选 `proofofwork`。若需要 PoW，使用 `seed` 和 `difficulty` 生成 `gAAAAAB...~S` 形态 token；否则使用新的 requirements token。最终 header 值是紧凑 JSON：

```json
{
  "p": "<pow or requirements token>",
  "t": "",
  "c": "<server token>",
  "id": "<device_id>",
  "flow": "<flow>"
}
```

该 JSON 字符串放入：

```http
openai-sentinel-token: {"p":"...","t":"","c":"...","id":"...","flow":"..."}
```

同时可设置 cookie：

```http
oai-sc=0<server token>
```

Sentinel PoW 生成细节：

| 项 | 实现 |
|:--|:--|
| requirements token 前缀 | `gAAAAAC` |
| proof token 前缀 | `gAAAAAB` |
| 最大尝试次数 | 500000 |
| hash | FNV-1a 32-bit 变体，对 `seed + payload` 计算。 |
| 成功条件 | hash 十六进制前缀按 difficulty 长度比较，小于等于 difficulty。 |
| fallback | 超过尝试次数后返回 `gAAAAAB` + 固定错误前缀 + base64 `"null"`。 |

登录 Sentinel 的浏览器环境数组和 conversation requirements 的 PoW 环境数组不同：前者模拟 `sentinel.openai.com/sentinel/20260124ceb8/sdk.js`，默认 Chrome 145；后者从 `chatgpt.com` 首页脚本和 `data-build` 中抽取资源，默认 Edge 143。

## 13. 注册相关网页逆向

注册流程也属于 `auth.openai.com` 网页逆向，但不属于 conversation 必需路径。

关键顺序：

1. GET `/api/accounts/authorize?...`，参数同 Platform OAuth + PKCE，带 `login_hint=email`。
2. POST `/api/accounts/user/register`，header 带 `openai-sentinel-token`，flow 为 `username_password_create`：

```json
{"username": "<email>", "password": "<password>"}
```

3. GET `/api/accounts/email-otp/send` 发送验证码。
4. POST `/api/accounts/email-otp/validate` 校验验证码：

```json
{"code": "<otp>"}
```

如果第一次不成功，会补 `openai-sentinel-token`，flow 为 `authorize_continue` 后重试。

5. POST `/api/accounts/create_account`，flow 为 `oauth_create_account`：

```json
{"name": "<first last>", "birthdate": "YYYY-MM-DD"}
```

响应的 `continue_url` 中提取 authorization code。

6. POST `/api/accounts/oauth/token` 用 code + PKCE verifier 换 `access_token`、`refresh_token`、`id_token`。

### 13.1 注册请求通用实现细节

注册流程使用 `curl_cffi.requests.Session(impersonate="chrome", verify=False)`，可配置代理。普通请求封装 `request_with_local_retry()`：

| 项 | 值 |
|:--|:--|
| 默认超时 | 30 秒 |
| 默认重试 | 3 次 |
| 重试间隔 | 1 秒 |
| 返回 | `(response, "")` 或 `(None, last_error)` |

注册相关 JSON 请求会带 Datadog trace header：

```http
traceparent: 00-<uuidhex>-<parent-id-16hex>-01
tracestate: dd=s:1;o:rum
x-datadog-origin: rum
x-datadog-parent-id: <parent-id>
x-datadog-sampling-priority: 1
x-datadog-trace-id: <trace-id>
```

Cloudflare 拦截检测：

| 信号 | 判断 |
|:--|:--|
| response header `server` 包含 `cloudflare` | 命中。 |
| HTML/text 包含 `challenges.cloudflare.com` | 命中。 |
| HTML/title 包含 `<title>just a moment` | 命中。 |

响应调试信息会提取最终 URL、`content-type`、`cf-ray`、`x-request-id`、`openai-processing-ms`、JSON body 或文本 body 前 800 字符。

注册流程通用 header 分两组：

JSON 请求 header：

```http
accept: application/json
accept-encoding: gzip, deflate, br
accept-language: en-US,en;q=0.9
cache-control: no-cache
connection: keep-alive
content-type: application/json
dnt: 1
origin: https://auth.openai.com
priority: u=1, i
sec-gpc: 1
sec-ch-ua: "Google Chrome";v="145", "Not?A_Brand";v="8", "Chromium";v="145"
sec-ch-ua-arch: "x86_64"
sec-ch-ua-bitness: "64"
sec-ch-ua-full-version-list: "Chromium";v="145.0.0.0", "Not:A-Brand";v="99.0.0.0", "Google Chrome";v="145.0.0.0"
sec-ch-ua-mobile: ?0
sec-ch-ua-model: ""
sec-ch-ua-platform: "Windows"
sec-ch-ua-platform-version: "10.0.0"
sec-fetch-dest: empty
sec-fetch-mode: cors
sec-fetch-site: same-origin
user-agent: Mozilla/5.0 ... Chrome/145.0.0.0 Safari/537.36
```

Navigation 请求 header：

```http
accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8
accept-encoding: gzip, deflate, br
accept-language: en-US,en;q=0.9
cache-control: max-age=0
connection: keep-alive
dnt: 1
sec-gpc: 1
sec-ch-ua: <同上>
sec-ch-ua-arch: "x86_64"
sec-ch-ua-bitness: "64"
sec-ch-ua-full-version-list: <同上>
sec-ch-ua-mobile: ?0
sec-ch-ua-model: ""
sec-ch-ua-platform: "Windows"
sec-ch-ua-platform-version: "10.0.0"
sec-fetch-dest: document
sec-fetch-mode: navigate
sec-fetch-site: same-origin
sec-fetch-user: ?1
upgrade-insecure-requests: 1
user-agent: Mozilla/5.0 ... Chrome/145.0.0.0 Safari/537.36
```

## 14. 关键反混淆/鲁棒性规则

1. 每次 conversation 前必须 bootstrap 首页并重新构造 requirements；requirements token 不能跨未知时长假定可复用。
2. `X-OpenAI-Target-Path` / `X-OpenAI-Target-Route` 是网页请求的重要 header，conversation 文档和下载接口应使用模板 route。
3. 图片输出必须结合消息角色、`async_task_type=image_gen`、`tool_invoked` 和“非用户消息”判断，不能仅凭 `file_` 或 `sediment://` 字符串。
4. 用户上传图片和图片工具输出都可能使用 `file-service://` / `sediment://`，必须区分输入与输出。
5. SSE 可能在图片实际生成完成前结束；有 `conversation_id` 时应轮询 conversation 文档和必要时 tasks。
6. `server_ste_metadata.tool_invoked == true` 不等于已有图片，只表示上游认为调用过工具。
7. `did_prompt_contain_image == true` 只表示输入有图，不可当作输出图片信号。
8. 内容策略拒绝可能只表现为 assistant 文本、`moderation.blocked` 或 tasks 结构化错误。
9. 文件下载接口返回的是二跳 URL；第一跳接口只返回 `download_url` / `url`，第二跳才是实际二进制资源。
10. OAuth authorization code 必须与同一 PKCE `code_verifier` 配对；`state` 可用于恢复会话和防错配。
11. `chatgpt.com` conversation PoW、`sentinel.openai.com` 登录 Sentinel PoW 是两套不同算法和环境，不可混用。
12. 远程图片 URL 是本项目先下载再上传给 ChatGPT 网页端；ChatGPT conversation 请求体里最终出现的是上传后的 `file-service://` 或 `sediment://` 指针，不是原始远程 URL。
13. 手动 OAuth 失败时本地 PKCE session 不应立即删除；同一个 verifier 仍可用于用户重新粘贴正确 callback。

## 15. 代码索引

| 文件 | 逆向内容 |
|:--|:--|
| `upstream/chatgpt2api/services/openai_backend_api.py` | ChatGPT 网页后端主封装：headers、bootstrap、requirements、conversation、图片、搜索、文件上传下载、账号信息。 |
| `upstream/chatgpt2api/services/protocol/conversation.py` | Conversation SSE 状态机、文本 patch、图片输出识别、轮询和结果整理。 |
| `upstream/chatgpt2api/utils/pow.py` | `chatgpt.com` conversation requirements / proof token 的 PoW 构造。 |
| `upstream/chatgpt2api/utils/sentinel.py` | `sentinel.openai.com` 登录/注册 Sentinel token 构造。 |
| `upstream/chatgpt2api/services/oauth_login_service.py` | 手动 OAuth + PKCE 授权码换 token。 |
| `upstream/chatgpt2api/services/register/openai_register.py` | 注册、OTP、账号资料创建、Platform OAuth token 兑换。 |
| `upstream/chatgpt2api/services/account_service.py` | refresh_token 刷新 access_token、密码重新登录的网页认证细节。 |
| `upstream/chatgpt2api/docs/upstream-sse-conversation.md` | 仓库已有 SSE 事件说明，可作为 Conversation SSE 交叉参考。 |
