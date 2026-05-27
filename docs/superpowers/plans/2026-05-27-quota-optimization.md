# 额度模块优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 借鉴上游项目优化 UAPI 的额度系统：429 分层重试、前端可视化增强、403 软结果、模型显示过滤、auto-disable 闭环、额度告警。

**Architecture:** 在现有 `internal/quota` 包基础上扩展。relay 层增加 429 智能重试。前端增强额度展示。admin 增加告警和账户状态持久化。

---

### Task 1: 前端 — Tier 徽章 + 相对倒计时 + 刷新按钮反馈

**Files:**
- Modify: `web/components/admin-channel-console.tsx`

**Changes:**

1. **Tier 徽章**：在账户卡片头部，`cred_type` badge 旁边，加一个 tier badge：
   - 从 `meta.quota.tier` 读取
   - 颜色映射：ULTRA/Max=紫色, PRO/Team/Enterprise=蓝色, FREE/空=灰色
   - 文本映射：ULTRA→U, PRO→P, Max→Max, Team→T, Enterprise→E, FREE→F

2. **相对倒计时**：重写 `formatResetTimeShort` 函数，返回相对时间：
   - <1h: "Xm" (分钟)
   - <24h: "Xh Xm"
   - <7d: "Xd Xh"
   - >=7d: "Xd"
   - 颜色分档：<1h=绿色, 1-6h=琥珀, >6h=灰色

3. **进度条三档色**：修改 `quotaTone` 函数：
   - >=50%: "high" (绿色)
   - >=20%: "medium" (琥珀)
   - <20%: "low" (红色)

4. **刷新按钮反馈**：
   - 添加 `refreshingIds` state (Set<string>)
   - 点击刷新时将 account.id 加入 set，完成后移除
   - 按钮在 refreshing 时显示 CSS spinner 动画代替 ↻
   - 添加 .quota-refresh-btn.spinning CSS 动画

5. **账户状态展示增强**：
   - 403/invalid 账户在 quota 区域显示错误状态文本而不是空
   - 当 `meta.quota` 存在但 buckets 为空且 tier 为空时，显示 "账户异常"

---

### Task 2: 429 分层重试

**Files:**
- Modify: `internal/relay/handler.go`

**Changes in `handleBuffered`:**

在现有的 `upResp.StatusCode() == 429` 分支中，增加智能重试逻辑：

```go
} else if upResp.StatusCode() == 429 {
    // Trigger quota refresh
    if r.quotaScheduler != nil && currentAccount != nil && ch != nil {
        r.quotaScheduler.On429(currentAccount.ID, ch.ID)
    }
    // Parse 429 response for retry strategy
    respBody429 := copyBody(upResp)
    statusCode = 429
    copyHeaders(upResp, &respHeaders)
    retryDelay := parseRetryDelay(respBody429, ch.APIFormat)
    if retryDelay >= 0 && retryDelay <= 3*time.Second && retry < 2 {
        // Short delay: wait and retry same account
        time.Sleep(retryDelay)
        fasthttp.ReleaseResponse(upResp)
        continue // retry with same account
    }
    // Medium/long delay: switch account
    shouldRetry = true
}
```

新增辅助函数：

```go
func parseRetryDelay(body []byte, apiFormat string) time.Duration {
    // Try to extract retry delay from response body
    // Gemini: look for "retryDelay" or "retry_info.retry_delay"
    // Anthropic: look for "error.retry_after"
    // Default: -1 (unknown, treat as long delay)
    ...
}
```

---

### Task 3: 403 软结果 + QuotaData 增加 Forbidden 字段

**Files:**
- Modify: `internal/quota/types.go`
- Modify: `internal/quota/antigravity.go`
- Modify: `internal/quota/gemini.go`

**Changes:**

1. `QuotaData` 增加字段：
```go
type QuotaData struct {
    Buckets     []QuotaBucket `json:"buckets"`
    Credits     *CreditsInfo  `json:"credits,omitempty"`
    Tier        string        `json:"tier,omitempty"`
    IsForbidden bool          `json:"is_forbidden,omitempty"`
    ForbiddenReason string    `json:"forbidden_reason,omitempty"`
    FetchedAt   time.Time     `json:"fetched_at"`
}
```

2. Antigravity Fetcher：403 时返回 `QuotaData{IsForbidden: true, ForbiddenReason: "account forbidden"}` 而不是 error

3. Gemini Fetcher：同上，403 时返回软结果

4. 前端：当 `meta.quota.is_forbidden` 为 true 时，账户卡片显示 "账户被禁" 红色状态

---

### Task 4: 模型显示过滤

**Files:**
- Modify: `web/components/admin-channel-console.tsx`

**Changes:**

1. 在账户卡片 quota 区域，默认只显示核心模型（最多 3 个）：
   - Claude: opus, sonnet
   - Gemini: pro, flash
   - Codex: 主窗口, 周窗口
   - 其他模型折叠

2. 当 quota buckets > 3 时，显示 "还有 N 个模型" 链接，点击展开全部

3. 添加 `showAllQuotaModels` state (per-account)，用 Set<string> 追踪哪些账户展开了全部模型

---

### Task 5: Auto-Disable 闭环 + 禁用原因持久化

**Files:**
- Modify: `internal/relay/handler.go`
- Modify: `internal/relay/pool.go`

**Changes:**

1. 在 `cooldownAndEvict` 或新的 429 处理中，当判断为真正配额耗尽时，将禁用原因写入 account metadata：
```go
acc.Metadata["auto_disable_reason"] = "quota_exhausted"
acc.Metadata["auto_disable_time"] = time.Now().UTC().Format(time.RFC3339)
```

2. 在请求成功时，如果 metadata 中有 `auto_disable_reason`，清除它：
```go
delete(acc.Metadata, "auto_disable_reason")
delete(acc.Metadata, "auto_disable_time")
```

3. 前端：读取 `meta.auto_disable_reason` 显示禁用原因标签

---

### Task 6: 额度告警通知

**Files:**
- Modify: `internal/quota/scheduler.go`
- Modify: `internal/admin/handler.go` or new file

**Changes:**

1. 在 `scheduler.refreshOne` 成功获取额度后，检查所有 bucket 的 `remaining_percent`：
   - 如果所有 bucket 都 <= 20%，记录一条告警日志
   - 将告警状态写入 `meta.quota_alert`：`{level: "warning", message: "所有模型额度低于 20%"}`

2. 前端：当 `meta.quota_alert` 存在时，在账户卡片显示告警图标

---

### Task 7: 401 Token 刷新重试（Scheduler 中）

**Files:**
- Modify: `internal/quota/scheduler.go`

**Changes:**

在 `refreshOne` 中，当 fetcher 返回的错误暗示 token 过期（401）时：
1. 调用 `oauthprovider.Get(provider).SyncMetadata(accessToken, metadata)` 尝试刷新
2. 如果刷新成功，用新 token 重试 `FetchQuota`
3. 如果刷新也失败（403），标记 `IsForbidden: true`

这需要 scheduler 知道 account 的 oauth provider。从 channel 的 APIFormat 推导。
