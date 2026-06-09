package relay

import (
	"sync"
	"time"
)

// CooldownPolicy 管理账号失败后的内存级指数退避状态。
// 所有 cooldown 时长 cap 15min，避免账号少的部署被锁死。
//
// 设计要点：
//   - 状态仅在内存，重启重置（首次稍慢可接受）
//   - 成功响应、手动清除、账号删除时调用 Reset
//   - 后台 GC 每 10min 清理 1h 未触发的状态，防泄漏
//   - 终态认证错误（ErrAccountTerminal）返回 0，由调用方走 disableAndEvict 路径
type CooldownPolicy struct {
	mu       sync.Mutex
	backoffs map[string]*accountBackoff // accountID -> backoff state
	stopGC   chan struct{}
	stopOnce sync.Once
}

type accountBackoff struct {
	level      int       // 当前退避级别（0 表示首次）
	lastFailAt time.Time // 用于 GC 判定空闲
}

// 退避序列（429 用）：10s → 30s → 2min → 5min → 15min
var backoffSteps = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

// CooldownCap 是所有 cooldown 时长的硬上限。
const CooldownCap = 15 * time.Minute

// gcInterval 是 GC 协程的执行周期。
const gcInterval = 10 * time.Minute

// gcIdleThreshold 是退避状态被清理的空闲时长阈值。
const gcIdleThreshold = 1 * time.Hour

// NewCooldownPolicy 创建并启动 cooldown 策略实例。
func NewCooldownPolicy() *CooldownPolicy {
	p := &CooldownPolicy{
		backoffs: make(map[string]*accountBackoff),
		stopGC:   make(chan struct{}),
	}
	go p.gcLoop()
	return p
}

// ComputeCooldown 根据错误类别和状态码计算 cooldown 时长。
// 返回 0 表示不需要 cooldown（服务器侧错误、终态错误、配置错误、客户端错误）。
//
// 注意：调用此方法本身会推进退避级别（仅对 429）。所以应当只在确认要 cooldown 时才调用。
func (p *CooldownPolicy) ComputeCooldown(class ErrorClass, statusCode int, accountID string) time.Duration {
	switch class {
	case ErrServerSide, ErrConfigSide, ErrClientSide, ErrAccountTerminal, ErrUnknown:
		return 0
	case ErrAccountSide:
		// 进入下方按状态码分发
	default:
		return 0
	}

	var d time.Duration
	switch statusCode {
	case 401:
		d = 1 * time.Minute
	case 402:
		d = 5 * time.Minute
	case 403:
		d = 15 * time.Minute
	case 429:
		d = p.nextBackoff(accountID)
	default:
		// AccountSide 但状态码不在表内，给个保守的 1min
		d = 1 * time.Minute
	}

	if d > CooldownCap {
		d = CooldownCap
	}
	return d
}

// nextBackoff 推进账号的指数退避并返回下一次 cooldown 时长。
func (p *CooldownPolicy) nextBackoff(accountID string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	b, ok := p.backoffs[accountID]
	if !ok {
		b = &accountBackoff{}
		p.backoffs[accountID] = b
	}

	idx := b.level
	if idx >= len(backoffSteps) {
		idx = len(backoffSteps) - 1
	}
	d := backoffSteps[idx]

	// 推进到下一级（封顶在最后一级）
	if b.level < len(backoffSteps)-1 {
		b.level++
	}
	b.lastFailAt = time.Now()

	return d
}

// Reset 清除某账号的退避状态。
// 应在以下场景调用：
//   - 账号成功响应（重置历史失败记录）
//   - 管理员手动清除/重新启用
//   - 账号被删除
//   - 账号进入终态 disable（disableAndEvict 时调用，避免无效内存占用）
func (p *CooldownPolicy) Reset(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.backoffs, accountID)
}

// Snapshot 返回当前所有退避状态的只读快照，供调试/观察使用。
func (p *CooldownPolicy) Snapshot() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.backoffs))
	for id, b := range p.backoffs {
		out[id] = b.level
	}
	return out
}

// gcLoop 后台循环清理空闲状态，防内存泄漏。
func (p *CooldownPolicy) gcLoop() {
	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopGC:
			return
		case <-ticker.C:
			p.gc()
		}
	}
}

func (p *CooldownPolicy) gc() {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-gcIdleThreshold)
	for id, b := range p.backoffs {
		if b.lastFailAt.Before(cutoff) {
			delete(p.backoffs, id)
		}
	}
}

// Close 停止后台 GC 协程。可安全重复调用。
func (p *CooldownPolicy) Close() {
	p.stopOnce.Do(func() {
		close(p.stopGC)
	})
}
