---
title: cli-relay v2 高性能 API 网关设计
date: 2026-05-13
status: approved
---

# cli-relay v2 — 高性能 API 中转网关设计文档

## 1. 项目定位

cli-relay 是一个**高性能 API 中转网关**，为下游用户提供统一的 OpenAI 兼容接口，透明转发到 OpenAI/Codex、Gemini、Anthropic 等上游服务。

核心能力：
- 透明代理 + 格式转换（下游 OpenAI 格式 → 上游原生格式）
- 号池管理（多账号加权轮询 + 故障自动切换）
- 双模式计费（次数窗口限额 + Token 额度扣费）
- 简单 Web 管理界面

## 2. 整体架构

```
下游客户端 (OpenAI 兼容格式, Bearer sk-xxx)
    │
    │  HTTPS
    ▼
┌──────────────────────────────────────────┐
│  nginx (TLS 终止, 限流, 静态缓存)          │
│    ↓ proxy_pass                          │
│  cli-relay (单 fasthttp 服务器, 单端口)     │
│                                          │
│  /               → Web UI (静态文件)       │
│  /api/admin/*    → 管理 API               │
│  /v1/*           → 转发引擎 (高性能核心)    │
│                                          │
│  ┌─────────────────────────────────────┐ │
│  │ 转发引擎流水线:                       │ │
│  │ ① 令牌验证                           │ │
│  │ ② 模型路由 → 渠道类型                 │ │
│  │ ③ 号池加权轮询选号                    │ │
│  │ ④ Adaptor 格式转换                    │ │
│  │ ⑤ 注入上游认证                       │ │
│  │ ⑥ SSE 流式转发 (零拷贝)              │ │
│  │ ⑦ 计费扣费 + 用量统计                 │ │
│  │ ⑧ 失败自动切换号重试                  │ │
│  └─────────────────────────────────────┘ │
│    │                                     │
│    ▼                                     │
│  PostgreSQL (MVCC 并发, 计费事务)         │
└──────────────────────────────────────────┘
        │           │           │
        ▼           ▼           ▼
   OpenAI/Codex   Gemini    Anthropic
```

## 3. 技术栈

| 组件 | 技术选型 | 理由 |
|------|---------|------|
| HTTP 框架 | fasthttp (valyala/fasthttp) | 转发核心需要高性能，~10x net/http |
| 数据库 | PostgreSQL 16+ | MVCC 并发能力强，计费事务安全 |
| ORM | GORM v2 | 成熟的 PG 支持，连接池，迁移 |
| 主键 | UUID v7 | 时间有序，B-tree 友好，无空洞心理预期 |
| 前端 | HTML + vanilla JS + Tailwind CSS | 无 Node.js 构建链，Go embed 嵌入 |
| 部署 | Docker Compose (cli-relay + PG + nginx) | 后期统一容器化 |

## 4. 数据模型

所有 ID 使用 UUID v7，`gen_random_uuid()` 生成。

### 4.1 channels — 渠道（号池分组）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID PK | |
| name | VARCHAR(64) | 渠道名称 |
| type | VARCHAR(16) | openai / gemini / anthropic |
| base_url | TEXT | 上游基础 URL |
| status | VARCHAR(16) | enabled / disabled |
| weight | INT | 渠道整体权重（跨渠道调度用） |
| priority | INT | 优先级（同模型多渠道时） |
| deleted_at | TIMESTAMPTZ NULL | soft delete，定时清理 |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

### 4.2 accounts — 号（渠道内的具体上游账号）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID PK | |
| channel_id | UUID FK → channels | 所属渠道 |
| name | VARCHAR(64) | 显示名称 |
| status | VARCHAR(16) | active / cooldown / disabled |
| auth_type | VARCHAR(16) | api_key / oauth |
| credentials | BYTEA | AES-256-GCM 加密的认证信息 |
| account_info | JSONB | 邮箱、plan类型等显示信息 |
| weight | INT | 轮询权重，冷却时置 0 |
| original_weight | INT | 正常权重值，冷却恢复用 |
| cooldown_until | TIMESTAMPTZ NULL | 冷却截止时间 |
| total_requests | BIGINT | 累计请求数 |
| total_input_tokens | BIGINT | 累计输入 token |
| total_output_tokens | BIGINT | 累计输出 token |
| last_used_at | TIMESTAMPTZ NULL | |
| last_error_at | TIMESTAMPTZ NULL | |
| last_error_msg | TEXT NULL | |
| deleted_at | TIMESTAMPTZ NULL | soft delete |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

### 4.3 tokens — 下游令牌（分发给用户的 key）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID PK | |
| key | VARCHAR(64) UNIQUE | sk-xxx 格式，API 认证用 |
| name | VARCHAR(64) | 令牌名称/备注 |
| plan_id | UUID FK → plans | 绑定的套餐 |
| status | VARCHAR(16) | enabled / disabled / exhausted |
| ip_whitelist | TEXT NULL | IP 白名单，逗号分隔 |
| deleted_at | TIMESTAMPTZ NULL | soft delete |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

### 4.4 plans — 套餐/计费方案

| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID PK | |
| name | VARCHAR(64) | 套餐名称 |
| type | VARCHAR(16) | count_based / token_based |
| price | DECIMAL(10,2) | 售价（展示用） |
| limits | JSONB NULL | 次数模式: `[{"window":"1h","max":100},{"window":"7d","max":1200}]` |
| token_quota | BIGINT NULL | Token 模式总额度 |
| model_ratios | JSONB NULL | 模型倍率: `{"gpt-4o":1,"claude-3.5":2,"gemini-pro":0.5}` |
| completion_ratio | DECIMAL(4,2) DEFAULT 3.0 | output/input 倍率 |
| duration_days | INT | 套餐有效天数 |
| status | VARCHAR(16) | enabled / disabled |
| deleted_at | TIMESTAMPTZ NULL | soft delete |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

### 4.5 token_plans — 令牌套餐实例（每令牌的实际用量跟踪）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID PK | |
| token_id | UUID FK → tokens | |
| plan_id | UUID FK → plans | |
| remaining_quota | BIGINT | Token 模式剩余额度 |
| used_quota | BIGINT | Token 模式已用额度 |
| window_usage | JSONB | 次数模式各窗口已用量 |
| window_reset_at | JSONB | 各窗口重置时间 |
| started_at | TIMESTAMPTZ | 生效时间 |
| expires_at | TIMESTAMPTZ | 过期时间 |

### 4.6 logs — 调用日志（按月分区）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGSERIAL | 分区表用自增即可 |
| token_id | UUID | |
| account_id | UUID | |
| channel_id | UUID | |
| model | VARCHAR(64) | |
| request_type | VARCHAR(16) | chat / embedding / image / audio |
| input_tokens | INT | |
| output_tokens | INT | |
| quota_cost | DECIMAL(12,4) | |
| latency_ms | INT | |
| status | VARCHAR(16) | success / fail / timeout |
| error_message | TEXT NULL | |
| created_at | TIMESTAMPTZ | 分区键 |

```sql
CREATE TABLE logs (...) PARTITION BY RANGE (created_at);
-- 每月一个分区，保留 6 个月，过期 DROP PARTITION
```

### 4.7 audit_log — 审计日志（按月分区）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGSERIAL | |
| admin_id | UUID | |
| action | VARCHAR(32) | login / create_channel / delete_token / ... |
| target_type | VARCHAR(32) | channel / account / token / plan |
| target_id | UUID | |
| detail | JSONB NULL | 变更详情 |
| ip | VARCHAR(45) | |
| created_at | TIMESTAMPTZ | 分区键 |

## 5. 删除策略

| 表 | 策略 | 说明 |
|---|---|---|
| channels / accounts / tokens / plans | soft delete + 30 天定时硬清理 | 管理 API 删除操作需 `confirm: true` |
| token_plans | 硬删除 | 套餐变更直接替换 |
| logs / audit_log | 按月分区 + DROP 旧分区 | 零碎片，保留 6 个月 |

定时清理任务（每天凌晨）：
1. `DELETE FROM {table} WHERE deleted_at < now() - INTERVAL '30 days'`
2. `DROP TABLE logs_{old_month}`（超过 6 个月的分区）
3. `VACUUM`

## 6. 转发引擎

### 6.1 Adaptor 接口

```go
type Adaptor interface {
    ConvertRequest(ctx context, openaiReq *OpenAIRequest) (any, error)
    ConvertResponse(ctx context, upstreamResp io.Reader) (*OpenAIResponse, error)
    ConvertStreamChunk(ctx context, chunk []byte) ([]byte, error)
    GetUpstreamURL(model string, endpoint string) string
    BuildAuthHeaders(account *Account) map[string]string
    GetSupportedEndpoints() []string
}
```

三种实现：

| 渠道类型 | 上游端点 | 认证头 | 格式转换要点 |
|----------|----------|--------|-------------|
| openai | `POST /v1/responses` | `Authorization: Bearer {access_token}` | messages → input, tools 映射 |
| gemini | `POST /v1/models/{model}:streamGenerateContent?alt=sse` | `x-goog-api-key: {api_key}` | messages → contents, roles 映射, tools → functionDeclarations |
| anthropic | `POST /v1/messages` | `x-api-key: {api_key}` + `anthropic-version` | system 提取到顶层, tools 格式微调 |

### 6.2 SSE 流式转发

```
上游 SSE → Adaptor.ConvertStreamChunk() → OpenAI SSE 格式 → 零拷贝透传下游
```

参考 Bifrost 的 SSEStreamReader：
- 使用 buffered channel（容量 1）实现 producer/consumer 并行
- 每次读一个完整 SSE event，不分批
- context 取消时正确 drain channel

### 6.3 性能优化（参考 Bifrost）

- `sync.Pool` 池化请求/响应对象，减少 GC 压力
- 连接复用：fasthttp 自带连接池
- 大体量请求体流式处理，不全量读入内存
- 令牌验证结果内存缓存（TTL 30s），减少 DB 查询

## 7. 号池调度

### 7.1 Smooth Weighted Round Robin

```go
type WeightedAccount struct {
    Account         *Account
    Weight          int     // 有效权重，冷却时为 0
    CurrentWeight   int     // 调度用临时权重
    OriginalWeight  int     // 配置的原始权重
}

type AccountPicker struct {
    mu          sync.RWMutex
    accounts    []WeightedAccount  // copy-on-write 热更新
    totalWeight int
}
```

算法：
1. 每个 account 的 `CurrentWeight += Weight`
2. 选 `CurrentWeight` 最大的
3. 被选中的 `CurrentWeight -= totalWeight`
4. 并发请求自然分散到所有有权重的号

### 7.2 故障切换

```
号A调用失败
  → 该号 Weight = 0，设 cooldown_until = now + 5min
  → 自动选下一个号重试（同一请求，不重复计费）
  → 所有号 Weight = 0 → 返回 503 + 管理员告警

冷却到期恢复：
  → 定时器检查 cooldown_until 已过 → 恢复 Weight = OriginalWeight
```

### 7.3 热更新

管理界面修改号/权重后，构建新 `[]WeightedAccount` 整体替换（copy-on-write），无锁竞争。

## 8. 计费系统

### 8.1 次数模式（count_based）

**窗口配置 — 自由组合，管理员自定义窗口大小和额度：**
```json
[
  {"window": "1h",  "max": 100},
  {"window": "5h",  "max": 500},
  {"window": "7d",  "max": 1200},
  {"window": "14d", "max": 2400},
  {"window": "30d", "max": 3000}
]
```

窗口 duration 格式：数字 + 单位（h=小时, d=天, w=周），支持任意组合如 `3h`、`5h`、`1w`、`2w`、`30d`。

**检查流程：**
1. 内存缓存各窗口计数器（per token）：`map[token_id]map[window_key]{count, resetAt}`
2. 检查 `resetAt`，过期则归零并更新
3. 所有窗口 `count < max` → 放行，各窗口 +1
4. 任一窗口 `count >= max` → 返回 `429 Too Many Requests` + `Retry-After` header

**持久化：** 每 30s 将内存计数器刷到 PG `token_plans.window_usage`

### 8.2 Token 模式（token_based）

**扣费公式：**
```
quota_cost = (input_tokens + output_tokens × completion_ratio) × model_ratio
```

**流程：**
1. 转发前：`SELECT remaining_quota FROM token_plans WHERE token_id = ? FOR UPDATE`
2. 检查 `remaining_quota > 0`，预扣估算额度
3. 转发完成后拿到实际用量：
   ```sql
   UPDATE token_plans
   SET remaining_quota = remaining_quota - actual_cost,
       used_quota = used_quota + actual_cost
   WHERE token_id = ?
   ```
4. 失败则退还预扣

**并发安全：** PG 行锁（`SELECT FOR UPDATE`）保证原子性

### 8.3 模型倍率

```json
{
  "gpt-4o": 1.0,
  "gpt-4o-mini": 0.3,
  "o3": 3.0,
  "claude-sonnet-4-20250514": 2.0,
  "gemini-2.5-pro": 1.5,
  "gemini-2.5-flash": 0.5
}
```

配置在 plans.model_ratios 中，不同套餐可设不同倍率。

## 9. API 接口

### 9.1 转发接口（/v1/*）

```
POST   /v1/chat/completions         → 聊天补全（流式/非流式）
POST   /v1/completions              → 文本补全
POST   /v1/embeddings               → 向量嵌入
POST   /v1/images/generations       → 图片生成
POST   /v1/audio/speech             → 语音合成
POST   /v1/audio/transcriptions     → 语音识别
POST   /v1/audio/translations       → 语音翻译
POST   /v1/moderations              → 内容审核
GET    /v1/models                   → 模型列表
GET    /v1/models/{model}           → 模型详情
```

认证：`Authorization: Bearer sk-xxx`

错误码：
- `401` — 无效令牌
- `429` — 额度不足 + `Retry-After` header
- `503` — 所有号不可用

### 9.2 管理 API（/api/admin/*）

需 JWT 认证。

```
认证:
  POST   /api/admin/login
  POST   /api/admin/logout

渠道:
  GET    /api/admin/channels
  POST   /api/admin/channels
  GET    /api/admin/channels/{id}
  PUT    /api/admin/channels/{id}
  DELETE /api/admin/channels/{id}?confirm=true

号:
  GET    /api/admin/channels/{id}/accounts
  POST   /api/admin/channels/{id}/accounts
  GET    /api/admin/accounts/{id}
  PUT    /api/admin/accounts/{id}
  DELETE /api/admin/accounts/{id}?confirm=true
  POST   /api/admin/accounts/{id}/refresh
  POST   /api/admin/accounts/{id}/test

令牌:
  GET    /api/admin/tokens
  POST   /api/admin/tokens
  GET    /api/admin/tokens/{id}
  PUT    /api/admin/tokens/{id}
  DELETE /api/admin/tokens/{id}?confirm=true
  POST   /api/admin/tokens/{id}/reset

套餐:
  GET    /api/admin/plans
  POST   /api/admin/plans
  PUT    /api/admin/plans/{id}
  DELETE /api/admin/plans/{id}?confirm=true

统计:
  GET    /api/admin/stats/overview
  GET    /api/admin/stats/logs?page=&model=&token_id=&from=&to=
  GET    /api/admin/stats/usage?period=hourly|daily|monthly

系统:
  GET    /api/admin/system/info
```

## 10. 安全设计

| 威胁 | 防御 |
|------|------|
| 暴力破解 | 登录限流 5次/5min/IP + bcrypt |
| 令牌泄露 | sk-xxx 仅创建时展示一次完整值 |
| SQL 注入 | GORM 参数化查询 |
| XSS | CORS 严格配置 + nginx 防御 |
| CSRF | JWT + SameSite cookie |
| 凭证泄露 | credentials 用 AES-256-GCM 加密，密钥从环境变量读取 |
| 审计 | 关键操作写 audit_log（登录、增删改渠道/令牌/套餐） |
| IP 限制 | 令牌级 IP 白名单（可选） |

管理 API 认证：JWT（24h 有效期），载荷 `{admin_id, exp, iat}`

## 11. Web UI

简单管理面板，Go embed 嵌入静态文件，纯 HTML + vanilla JS + Tailwind CSS。

页面：
- **仪表盘** — 总请求数、今日 token、活跃令牌、渠道健康度
- **渠道管理** — 渠道列表 + 号池展开，显示每个号的状态/权重/用量
- **令牌管理** — 令牌列表，套餐绑定，用量展示
- **套餐管理** — 套餐 CRUD
- **调用日志** — 分页 + 过滤
- **系统设置** — 基本配置

## 12. 配置

```yaml
server:
  listen: ":8080"
  base_url: "https://api.example.com"

database:
  host: "localhost"
  port: 5432
  name: "cli_relay"
  user: "cli_relay"
  password: "${DB_PASSWORD}"
  max_open_conns: 20
  max_idle_conns: 10

security:
  encryption_key: "${ENCRYPTION_KEY}"
  admin_password: "${ADMIN_PASSWORD}"
  jwt_secret: "${JWT_SECRET}"

scheduler:
  token_refresh_interval: "1h"
  cleanup_interval: "24h"
  cleanup_retention: "30d"
  log_retention_months: 6

log:
  level: "info"
  format: "json"
```

## 13. 后期计划

- **容器化**：Docker Compose（cli-relay + PG + nginx），最终全部完成后统一处理
- **监控告警**：渠道全量不可用时通知管理员
- **更多上游**：按需添加 AWS Bedrock、Azure 等 Adaptor
