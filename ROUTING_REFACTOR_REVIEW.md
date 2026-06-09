# UAPI 路由与亲和性改造 - 验收检查提示词

本文档用于在改造完成后由独立 AI 助手对实现进行系统性审查。请把本文件全文作为提示词发给审查者。

---

## 任务说明

你是一名资深 Go 工程师 + 系统架构 reviewer。请对 UAPI 项目（位于当前工作目录）进行一次**路由 + 链路亲和 + Failover + 并发队列**改造的完整验收审查。

**审查原则**：
- 以**当前代码实际状态**为准，不要相信记忆/缓存中的旧版本
- 严格按下方"检查清单"逐条核对
- 对每条检查项给出 ✅ 通过 / ⚠️ 部分通过 / ❌ 未通过 / ⏭️ 跳过（理由）
- 发现的问题给出 `文件路径:行号` 精确定位
- 不修改任何代码，只产出审查报告

---

## 改造目标速览

| # | 改造点 | 目标 |
|---|---|---|
| A | 错误码语义分类 | 服务器侧 / 账号侧 / 终态认证 / 配置侧 / 客户端侧五类分发 |
| B | 分级 Failover | 服务器侧跳渠道，账号侧分散换账号 (min(3, available))，跨 channel 计数清零 |
| C | Cooldown 时长分级 | 401=1min / 402=5min / 403=15min / 429指数退避，全部 cap 15min |
| D | 终态认证错误 | invalid_grant/token_revoked 等关键词命中 → 直接 disable，不自动恢复 |
| E | AffinityCache 重构 | 反向索引 + SetIfAbsent + 读时 stale 校验 |
| F | 亲和路径走 quota 闸门 | pickAccountForAffinity 不再绕过 quota 检查 |
| G | Channel-Model Blocklist | 404 model 错误时标记 (channel, model)，5min TTL |
| H | 删除请求前预写 | 只在响应成功后写 affinity，不留僵尸记录 |
| I | tokenID fallback scope | 无 session 标识时用 token 作 scope |
| J | 动态并发队列 | 超过套餐并发上限时排队而非拒绝，支持取消 |
| K | 管理员手动恢复 | 复用账号启用按钮恢复，区分临时 cooldown / 认证重试 / 终态 disable |

---

## 检查清单

### Phase 1 - 基础设施模块

#### 1.1 错误码分类模块 `internal/relay/errclass.go`
- [ ] 文件存在，定义了 `ErrorClass` 枚举
- [ ] 包含五个枚举值：`ErrServerSide` / `ErrAccountSide` / `ErrAccountTerminal` / `ErrConfigSide` / `ErrClientSide`（外加 `ErrUnknown`）
- [ ] 提供 `ClassifyUpstreamError(statusCode int, body []byte) ErrorClass`
- [ ] 408/5xx → `ErrServerSide`
- [ ] 401 默认 `ErrAccountSide`，但响应体不含 auth/key/token 关键词时降级为 `ErrServerSide`
- [ ] 402/429 → `ErrAccountSide`
- [ ] 403 → `ErrAccountSide`
- [ ] 404 含 model/not_found 关键词 → `ErrConfigSide`，否则 `ErrClientSide`
- [ ] 400/422 → `ErrClientSide`
- [ ] 提供 `IsTerminalAuthError(statusCode int, body []byte) bool`
- [ ] 终态关键词覆盖：`invalid_grant` / `token_revoked` / `refresh_token_expired` / `account_disabled` / `account_suspended` / `account_terminated` / `banned` / `api_key_revoked` / `credential_invalid`
- [ ] 有对应单元测试 `errclass_test.go`，覆盖各状态码 + 关键词组合的边界 case
- [ ] 不依赖 db / pool / handler 包

#### 1.2 CooldownPolicy 模块 `internal/relay/cooldown.go`
- [ ] 文件存在，类型 `CooldownPolicy` 实现内存级退避状态管理
- [ ] `ComputeCooldown(class ErrorClass, statusCode int, accountID string) time.Duration` 接口存在
- [ ] 时长映射符合设计：401=1min / 402=5min / 403=15min / 429 指数退避（10s→30s→2min→5min→15min）
- [ ] 所有时长 cap 15min
- [ ] `ErrServerSide` / `ErrConfigSide` / `ErrClientSide` 返回 0（不 cooldown 账号）
- [ ] `ErrAccountTerminal` 返回 0（直接 disable，不走 cooldown）
- [ ] 提供 `Reset(accountID string)` 用于成功响应、手动清除、账号删除
- [ ] 后台 GC 协程每 10min 清理 1h 未触发的状态
- [ ] 提供 `Close()` 优雅停止 GC
- [ ] 退避状态用 mutex 保护，并发安全
- [ ] 有单元测试覆盖指数退避序列 / Reset / GC

#### 1.3 Channel-Model Blocklist 模块 `internal/relay/channel_model_block.go`
- [ ] 文件存在，类型 `ChannelModelBlocklist`
- [ ] `Block(channelID, model string)` / `IsBlocked(channelID, model string) bool` / `ClearChannel(channelID string)`
- [ ] TTL 默认 5min
- [ ] `IsBlocked` 检测到过期会自动清理
- [ ] mutex 保护并发安全
- [ ] 在 `channelSupportsModel`（handler.go:4264 附近）的判定链中被调用

### Phase 2 - AffinityCache 重构

#### 2.1 反向索引 `internal/relay/affinity.go`
- [ ] 数据结构包含 `byAccount map[string]map[string]struct{}` 和 `byChannel map[string]map[string]struct{}`
- [ ] `InvalidateAccount(accountID string)` 通过反向索引 O(scope_count) 清除
- [ ] `InvalidateChannel(channelID string)` 类似
- [ ] 写入/删除时三个索引同步维护（无脏数据）
- [ ] 旧 entry 被覆盖时从反向索引中移除
- [ ] 单元测试覆盖反向索引一致性

#### 2.2 SetIfAbsent / ForceSet 分离
- [ ] `SetIfAbsent(tokenID, model, scope, channelID, accountID, ttlSeconds) (chID, accID string, existed bool)` 存在
- [ ] 已存在有效 entry 时返回已有的，不覆盖
- [ ] `ForceSet(...)` 强制覆盖，用于 failover 成功后更新
- [ ] handler.go 中首次成功 → SetIfAbsent；failover 成功 → ForceSet
- [ ] 写入前检查 account/channel 仍有效（避免污染）

#### 2.3 Get 返回值与 Stale 校验
- [ ] `Get` 签名为 `(channelID, accountID string, hit bool)` 或等价
- [ ] `resolveChannelAndAccountWithAttempts` 中命中 affinity 后执行五项 stale 校验：
  - [ ] channel 仍存在且 enabled
  - [ ] `channelSupportsModel(ch, model)` 通过（含 blocklist 检查）
  - [ ] `channelSupportsCapability(ch, capabilityReq...)` 通过
  - [ ] account 仍存在且 enabled
  - [ ] account 不在 cooldown（pool 中 weight > 0）
- [ ] 任一不通过则调用 `InvalidateAccount` 反向清除并降级到正常路由

### Phase 3 - 核心 Failover 改造

#### 3.1 删除请求前预写 affinity
- [ ] `handler.go:484` 附近的 `recordSelectedAffinity` 预写入调用已删除
- [ ] 仅保留 success-only 写入（原 1001/1288/1865 等位置）
- [ ] 重试完全耗尽路径不会留下僵尸 entry

#### 3.2 pickAccountForAffinity 走 quota 闸门
- [ ] `pool.go` 新增 `PickByIDForModel(accountID, model string) (*db.Account, bool)`，在 weight 检查基础上追加 `modelQuotaExhausted` 检查
- [ ] `handler.go:2754` `pickAccountForAffinity` 改用 `PickByIDForModel`
- [ ] PickByIDForModel 返回 false 时降级到 `pool.PickForModel(model, nil)`
- [ ] 旧 `PickByID` 保留不删（兼容其他调用方）

#### 3.3 handleStreamingAttempt 按错误类分发
- [ ] `handler.go:649` 收到 upstream 错误后调用 `ClassifyUpstreamError`
- [ ] `ErrServerSide` 分支：`affinity.InvalidateChannel` + 不动账号 + channel 级 failover
- [ ] `ErrAccountSide` 分支：`ComputeCooldown` → `pool.Cooldown` → `affinity.InvalidateAccount` → 同 channel 内 attemptCount++ 或升级
- [ ] `ErrAccountTerminal` 分支：认证/令牌类错误立即调用 `disableAndEvict` 永久禁用账号，不再定时重试
- [ ] `ErrConfigSide` 分支：`blocklist.Block` + `affinity.InvalidateChannel` + channel 级 failover
- [ ] `ErrClientSide` 分支：直接返回错误给客户端，不重试
- [ ] OAuth refresh 失败升级为 `ErrAccountTerminal`

#### 3.4 账号尝试上限改 min(3, available) + 渠道遍历到耗尽
- [ ] `accountAttemptLimit` 计算改为 `min(3, pool.AvailableCount())`
- [ ] 跨到新 channel 时 `accountAttempts` 重置为 0
- [ ] 跨 channel 时 `excludeAccs` 重置（新 channel 新世界）
- [ ] 服务器侧错误直接放弃当前 channel，尝试下一个未失败 channel
- [ ] 账号侧错误在当前 channel 内最多尝试 3 个未标记异常账号；当前 channel 账号耗尽后切换下一个未失败 channel
- [ ] channel 级 failover 不再固定 3 次上限，而是遍历候选 channel 直到耗尽；所有 channel 不可用才返回异常

#### 3.5 prepareChannelFailover 加 affinity 清除
- [ ] `handler.go:3164` `prepareChannelFailover` 增加 `r.affinity.InvalidateChannel(ch.ID.String())`
- [ ] **不引入** channel 级 cooldown 概念
- [ ] 下一个请求该 channel 仍参与候选

#### 3.6 prepareAccountFailover 接入新 cooldown 策略
- [ ] `handler.go:2971` 改为调用 `CooldownPolicy.ComputeCooldown`
- [ ] 移除原 5min 硬编码
- [ ] 使用 `InvalidateAccount` 反向清除（而非旧 `EvictAccount` 单 scope 清）

#### 3.7 服务器侧错误不触发账号 cooldown
- [ ] 408/5xx 不调用任何 account 状态变更
- [ ] 仅 `EvictChannel` + 跳渠道
- [ ] 单元/集成测试验证：5xx 后账号 Weight 不变

#### 3.8 终态错误 disableAndEvict
- [ ] `handler.go` 新增 `disableAndEvict(acc, reason string)` 函数
- [ ] 额度耗尽类错误不走永久禁用，依赖额度桶 reset/cooldown 自动恢复
- [ ] 认证/令牌失效类错误立即写入 `disabled_reason` / `disabled_at` 并永久禁用
- [ ] 认证/令牌失效类错误不写入 `auth_failure_attempts`，不进入 cooldown 重试
- [ ] 调用 `pool.Disable(accountID)` 永久置 weight=0
- [ ] `acc.Enabled = false` 持久化到 DB
- [ ] 写入 `acc.Metadata["disabled_reason"]` 和 `disabled_at`
- [ ] 调用 `AffinityCache.InvalidateAccount` 清亲和
- [ ] 调用 `CooldownPolicy.Reset` 清退避状态
- [ ] 不调用 `time.AfterFunc` 自动恢复

### Phase 4 - 动态并发队列

#### 4.1 ConcurrencyLimiter 改造 `internal/relay/concurrency.go`
- [ ] 不再硬性拒绝超额请求，改为排队
- [ ] 每用户独立 FIFO 队列
- [ ] 使用 `select on ctx.Done()` 检测客户端取消
- [ ] 客户端取消时主动出队，不占配额
- [ ] 队列等待超时 30min，超时返回 503
- [ ] 单用户队列长度上限 50，超出返回 429
- [ ] `defer` 保证（含 panic 路径）信号量必释放
- [ ] 在 `HandleRelay` 中正确接入，排队期间不占用 channel/account
- [ ] 有单元测试：超额排队、客户端取消出队、超时、并发释放

### Phase 5 - Scope 兜底 + Admin

#### 5.1 tokenID fallback scope
- [ ] `requestAffinityScope`（handler.go:3174）在所有探测路径返回空时 fallback 到 `"token:" + tokenID`
- [ ] tokenID 也为空的极端情况才返回 ""

#### 5.2 后端账号启用即恢复
- [ ] 不新增 reactivate 兼容接口；复用现有 `PUT /api/admin/accounts?id=...` 的 `enabled=true`
- [ ] 启用账号时重置 `CooldownUntil = nil`、`Enabled = true`
- [ ] 启用账号时清除 metadata 中 `disabled_reason` / `disabled_at` / `auto_disable_reason` / `auth_failure_*` / `last_terminal_error_*`
- [ ] 启用账号时调用 `CooldownPolicy.Reset`
- [ ] 启用账号时调用 `pool.RestoreAccount` 或刷新 pool
- [ ] 启用账号时调用 `AffinityCache.InvalidateAccount`
- [ ] admin 鉴权中间件保护
- [ ] 并发安全（手动恢复与 time.AfterFunc 不冲突）

#### 5.3 后端 channel failure state
- [ ] 不保留旧 `clear-failure` 兼容接口
- [ ] 不要求前端/后端手动清除失败状态接口；`ChannelModelBlocklist` 依赖 5min TTL 自动恢复

#### 5.4 前端账号管理页
- [ ] 区分三种账号状态：normal (绿) / temporary cooldown (黄) / permanently disabled (红)
- [ ] 临时 cooldown 显示倒计时和触发状态码
- [ ] 认证失败状态显示为已禁用，并提示可通过账号启用按钮恢复
- [ ] 额度耗尽状态说明等待额度桶重置后自动恢复
- [ ] 终态 disabled 显示 disabled_reason 和 disabled_at，并提示可点击现有“启用”按钮恢复
- [ ] 不新增“立即恢复”按钮；现有启用按钮触发后端恢复语义

#### 5.5 前端渠道管理页
- [ ] 不要求“清除失败状态”按钮
- [ ] 显示该渠道下账号 cooldown 汇总

### Phase 6 - 观察性

#### 6.1 路由决策日志
- [ ] affinity 命中 / 未命中 / stale 失效有结构化日志
- [ ] channel 候选集大小、account 选择结果有日志
- [ ] failover 触发原因（errClass + statusCode）有日志
- [ ] cooldown 时长决策有日志
- [ ] 日志级别合理（debug/info）
- [ ] debug-dumps 模式下打到 dump 文件

---

## 横向验证项（贯穿全代码）

### 一致性检查
- [ ] 亲和路径与正常路径用同一套有效性闸门（quota / cooldown / capability / model）
- [ ] 错误处理统一通过 `ClassifyUpstreamError`，无散落的 `if statusCode == xxx` 处理
- [ ] 所有 cooldown 时长来自 `CooldownPolicy.ComputeCooldown`，无硬编码

### 并发安全检查
- [ ] `AffinityCache` 写入三个索引在同一把锁下完成
- [ ] `CooldownPolicy` 退避状态 mutex 保护
- [ ] `ChannelModelBlocklist` mutex 保护
- [ ] `ConcurrencyLimiter` 信号量释放幂等
- [ ] `pool.RestoreAccount` 与 `time.AfterFunc` 自动恢复幂等

### 内存安全检查
- [ ] `CooldownPolicy` 有 GC 防泄漏
- [ ] `ChannelModelBlocklist` 过期 entry 会被清理
- [ ] `ConcurrencyLimiter` 队列释放后引用清空
- [ ] 账号删除时 `CooldownPolicy.Reset` 被调用

### 边界 case
- [ ] 只有 1 个 channel 时：账号级 failover 用尽后不会无限重试
- [ ] 只有 1 个 account 时：账号级 failover 立即升级到 channel 级
- [ ] 所有 channel 都对某 model 标记 404 时：返回明确错误，不无限重试
- [ ] 所有账号都 cooldown 时：行为合理（按现有逻辑放行还是返回错误，需验证）
- [ ] OAuth refresh 并发：同账号只触发一次 refresh

### 回归检查
- [ ] 现有 `isUpstreamQuotaExhausted` / `terminalAccountDisableReason` 等辅助函数仍正确工作；不要求保留旧兼容接口/旧 failover 主路径
- [ ] 现有单元测试全部通过
- [ ] `debug-dumps` 中的历史失败 case 能成功 replay

---

## 已知限制（不在本次改造范围，无需报告）

- DB/Runtime 模式共享 AffinityCache（stale 校验兜底，安全）
- 模型别名导致 affinity key 分裂（影响轻微，不修）

---

## 输出要求

请按以下格式输出审查报告：

```
# UAPI 路由改造验收报告

## 总体结论
- 通过项：X/Y
- 部分通过：A 项
- 未通过：B 项
- 严重问题：C 项（需要立即修复）

## 详细结果

### Phase 1
1.1.1 ✅ ErrorClass 枚举定义齐全 (internal/relay/errclass.go:15-20)
1.1.2 ❌ ErrAccountTerminal 缺少 OAuth refresh 失败场景 (internal/relay/handler.go:702)
...

## 严重问题清单
1. [严重] xxx 文件:行 - 描述 - 建议修复
...

## 改进建议
1. xxx
...
```

务必精确到文件和行号，不要笼统描述。
