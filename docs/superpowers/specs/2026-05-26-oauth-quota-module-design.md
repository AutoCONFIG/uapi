# OAuth 渠道额度模块设计

## 目标

将各 OAuth 渠道（Gemini、Antigravity、Claude Code、Codex）的额度获取逻辑统一到 `internal/quota` 包，实现：

1. 标准化数据结构 — 所有渠道统一输出，前端只需渲染一种格式
2. 可扩展 — 新增渠道只需实现 `Fetcher` 接口并注册
3. 串行刷新 — 防止并发请求被上游封禁
4. 两个触发点 — 429 响应时 + 管理员前端访问时

## 标准数据结构

存入 `account.Metadata["quota"]`，所有渠道统一格式：

```go
type QuotaData struct {
    Buckets   []QuotaBucket `json:"buckets"`
    Credits   *CreditsInfo  `json:"credits,omitempty"`
    Tier      string        `json:"tier,omitempty"`       // FREE/PRO/ULTRA/Max/Team/Enterprise
    FetchedAt time.Time     `json:"fetched_at"`
}

type QuotaBucket struct {
    Label            string `json:"label"`                // "Gemini Pro", "Claude 5h", "Codex 周窗口"
    RemainingPercent int    `json:"remaining_percent"`    // 0-100
    ResetTime        string `json:"reset_time,omitempty"` // ISO 8601
}

type CreditsInfo struct {
    Balance   string `json:"balance,omitempty"`   // "25" 或 "unlimited"
    Unlimited bool   `json:"unlimited"`
    Label     string `json:"label,omitempty"`     // "G1 AI Credits"
}
```

原始上游数据保留在各自的 metadata key（`user_quota`、`usage`、`codex_usage` 等）供调试，`quota` 是标准化摘要。

## Fetcher 接口

```go
// internal/quota/fetcher.go

type Fetcher interface {
    FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error)
}
```

注册表按 `channel.APIFormat` 查找对应 Fetcher：

```go
var registry = map[string]Fetcher{}

func Register(apiFormat string, f Fetcher) { registry[apiFormat] = f }

func Get(apiFormat string) (Fetcher, bool) { f, ok := registry[apiFormat]; return f, ok }
```

各渠道在 `init()` 中注册自身。

## 各渠道实现

### Gemini (`quota/gemini.go`)

- **API**: `POST https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota`
- **转换**: `bucket.remainingFraction * 100` → `QuotaBucket.RemainingPercent`
- **Label**: 优先用 `bucket.modelId`，否则用 "额度桶 N"
- **Credits**: 从 `loadCodeAssist` 响应的 `paidTier.availableCredits` 提取，过滤 `creditType == "GOOGLE_ONE_AI"`
- **Tier**: `paidTier.id`

### Antigravity (`quota/antigravity.go`)

- **API**: `POST https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels`，带多端点 fallback（sandbox → daily → production，借鉴 Antigravity-Manager）
- **转换**: 每个模型的 `remaining_fraction * 100` → `QuotaBucket.RemainingPercent`
- **Label**: 用模型名
- **Credits**: 从 `loadCodeAssist` 的 `paidTier.availableCredits` 提取（同 Gemini 逻辑，复用）
- **Tier**: `paidTier.id`
- **Project ID**: 从 `metadata["project_id"]` 或 `metadata["load_code_assist"]["cloudaicompanionProject"]` 提取

### Anthropic / Claude Code (`quota/anthropic.go`)

- **API**: `GET https://api.claude.ai/api/oauth/usage`
- **转换**: 每个窗口 `100 - utilization` → `QuotaBucket.RemainingPercent`
- **Buckets**: `five_hour` → "Claude 5h 窗口"，`seven_day` → "Claude 周窗口"，`seven_day_sonnet` → "Claude Sonnet 周窗口"，`seven_day_opus` → "Claude Opus 周窗口"
- **Credits**: `extra_usage.used_credits` / `extra_usage.monthly_limit`
- **Tier**: 从 metadata 中的 `subscription_type` 获取

### Codex (`quota/codex.go`)

- **API**: `GET https://chatgpt.com/backend-api/api/codex/usage`
- **转换**: `100 - primary.used_percent` → "Codex 主窗口"，`100 - secondary.used_percent` → "Codex 周窗口"
- **Credits**: `credits.balance`，`credits.unlimited`
- **Tier**: 从 metadata 中的 `chatgpt_plan_type` 获取

## 刷新调度

### 触发点

1. **429 响应时** — relay 层捕获上游 429 后，投递该账户到刷新队列
2. **管理员前端访问时** — `POST /admin/accounts/:id/refresh-quota` 或 `POST /admin/channels/:id/refresh-quota`

### 调度器设计 (`scheduler.go`)

小批量 + 抖动：同一渠道的账户分成小批次（3-5 个）并行刷新，批次之间加随机抖动间隔（2-8 秒），避免被上游识别为异常访问。

```go
type Scheduler struct {
    db      *gorm.DB
    crypto  *crypto.Service
    mu      sync.Mutex        // 防止同一渠道并发刷新
    pending map[uuid.UUID]bool // 去重：正在刷新的账户
}

const (
    batchSize    = 3               // 每批 3 个账户并行
    jitterMin    = 2 * time.Second // 批次间最小间隔
    jitterMax    = 8 * time.Second // 批次间最大间隔
    stalenessTTL = 5 * time.Minute // 同一账户 5 分钟内不重复刷新
)

// RefreshChannel 刷新指定渠道下所有 OAuth 账户（小批量 + 抖动）
func (s *Scheduler) RefreshChannel(channelID uuid.UUID) error {
    // 1. 加载该渠道下所有 OAuth 账户
    // 2. 过滤掉 stalenessTTL 内已刷新的
    // 3. 分批：每批 batchSize 个，用 errgroup 并行
    // 4. 批次间 sleep random(jitterMin, jitterMax)
}

// RefreshAccount 刷新单个账户（管理员前端触发）
func (s *Scheduler) RefreshAccount(accountID uuid.UUID) (*QuotaData, error) {
    // 直接执行，不走队列（单账户无需排队）
}

// On429 429 触发刷新（投递到后台 goroutine，不阻塞请求）
func (s *Scheduler) On429(accountID, channelID uuid.UUID) {
    go s.RefreshAccount(accountID) // 异步执行，不阻塞 relay
}
```

### 批量刷新流程

```
RefreshChannel(channelID)
│
├── 加载渠道下所有 OAuth 账户，过滤 staleness
│
├── 批次 1: [acc1, acc2, acc3]  ← errgroup 并行
│   └── sleep random(2s, 8s)     ← 抖动
├── 批次 2: [acc4, acc5, acc6]  ← errgroup 并行
│   └── sleep random(2s, 8s)
├── ...
```

同一渠道的 RefreshChannel 调用通过 `sync.Mutex` 互斥，防止同一渠道并发刷新。不同渠道之间可以并行。

### Staleness Guard

同一账户 5 分钟内不重复刷新。在 `Enqueue` 时检查 `meta.quota.fetched_at`，如果距今不足 5 分钟则跳过。

### processOne 流程

每个账户的刷新逻辑：

1. 通过 `quota.Get(channel.APIFormat)` 查找 Fetcher
2. 解密 access token（如过期先刷新）
3. 调用 `Fetcher.FetchQuota(accessToken, account.Metadata)`
4. 将结果写入 `account.Metadata["quota"]`
5. 保存到 DB

### 429 触发集成

在 relay 层的错误处理中，当上游返回 429 时：

```go
if resp.StatusCode() == 429 {
    if quotaScheduler != nil {
        quotaScheduler.On429(account.ID, channel.ID)
    }
}
```

异步执行，不阻塞当前请求的响应。

## API 端点

| 方法 | 端点 | 用途 |
|------|------|------|
| POST | `/admin/accounts/:id/refresh-quota` | 刷新单个账户额度 |
| POST | `/admin/channels/:id/refresh-quota` | 串行刷新该渠道下所有 OAuth 账户 |

返回标准 `QuotaData`。

## 前端变化

### buildQuotaDisplayItems 简化

只读 `meta.quota`，不再按渠道分别解析：

```typescript
const quota = asRecord(meta.quota);
if (quota) {
    const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean);
    for (const [i, b] of buckets.entries()) {
        items.push({
            key: `quota-${i}`,
            label: stringValue(b.label) || `额度 ${i + 1}`,
            remainingPercent: numberValue(b.remaining_percent) ?? 0,
            resetText: formatResetTimeShort(stringValue(b.reset_time)),
        });
    }
    const credits = asRecord(quota.credits);
    if (credits) {
        const balance = stringValue(credits.balance) || (credits.unlimited ? "unlimited" : "");
        if (balance) {
            items.push({
                key: "quota-credits",
                label: stringValue(credits.label) || "Credits",
                remainingPercent: credits.unlimited ? 100 : 0,
                detail: `Credits ${balance}`,
            });
        }
    }
}
```

旧的 `user_quota` / `codex_usage` / `usage` 解析逻辑保留作为 fallback（`meta.quota` 不存在时走旧逻辑），实现向后兼容。

### 手动刷新按钮

在账户卡片上添加"刷新额度"按钮，调用 `POST /admin/accounts/:id/refresh-quota`。

## 文件结构

```
internal/quota/
├── types.go           # QuotaData, QuotaBucket, CreditsInfo
├── fetcher.go         # Fetcher 接口 + Registry
├── scheduler.go       # 串行刷新调度器 + staleness guard
├── gemini.go          # Gemini Fetcher（init 注册）
├── antigravity.go     # Antigravity Fetcher（init 注册，含多端点 fallback）
├── anthropic.go       # Claude Code Fetcher（init 注册）
└── codex.go           # Codex Fetcher（init 注册）
```

## 向后兼容

- 旧 metadata key（`user_quota`、`usage`、`codex_usage`）保留，新模块只在 `meta.quota` 写入标准化数据
- 前端 `buildQuotaDisplayItems` 优先读 `meta.quota`，不存在时 fallback 到旧逻辑
- 逐步迁移：旧逻辑不删除，待所有渠道都接入 `quota` 包后再移除
