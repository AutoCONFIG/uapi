package relay

import (
	"sync"
	"time"
)

// ChannelModelBlocklist 维护 (channelID, model) -> blockedUntil 的内存级标记。
// 用于 404 配置错误场景：某个 channel 不支持某个 model 时短期内不参与候选。
//
// 设计要点：
//   - TTL 默认 5min（短，便于自动恢复）
//   - IsBlocked 检测到过期会主动清理
//   - 提供 ClearChannel 用于管理员手动清除
//   - 不持久化，重启重置
type ChannelModelBlocklist struct {
	mu      sync.Mutex
	blocked map[modelBlockKey]time.Time // -> blockedUntil
	ttl     time.Duration
}

type modelBlockKey struct {
	channelID string
	model     string
}

// DefaultModelBlockTTL 是 404 配置错误的默认隔离时长。
const DefaultModelBlockTTL = 5 * time.Minute

// NewChannelModelBlocklist 创建实例。ttl <= 0 时使用 DefaultModelBlockTTL。
func NewChannelModelBlocklist(ttl time.Duration) *ChannelModelBlocklist {
	if ttl <= 0 {
		ttl = DefaultModelBlockTTL
	}
	return &ChannelModelBlocklist{
		blocked: make(map[modelBlockKey]time.Time),
		ttl:     ttl,
	}
}

// Block 标记 (channelID, model) 不可用，blockedUntil = now + ttl。
func (b *ChannelModelBlocklist) Block(channelID, model string) {
	if channelID == "" || model == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[modelBlockKey{channelID, model}] = time.Now().Add(b.ttl)
}

// IsBlocked 查询 (channelID, model) 是否处于隔离状态。
// 如果已过期，会主动从 map 中清除。
func (b *ChannelModelBlocklist) IsBlocked(channelID, model string) bool {
	if channelID == "" || model == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.blocked[modelBlockKey{channelID, model}]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(b.blocked, modelBlockKey{channelID, model})
		return false
	}
	return true
}

// ClearChannel 手动清除某 channel 的所有 model 标记。
// 用于管理员"清除失败状态"接口。
func (b *ChannelModelBlocklist) ClearChannel(channelID string) int {
	if channelID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for k := range b.blocked {
		if k.channelID == channelID {
			delete(b.blocked, k)
			count++
		}
	}
	return count
}

// ClearAll 清除所有标记。用于管理或测试。
func (b *ChannelModelBlocklist) ClearAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked = make(map[modelBlockKey]time.Time)
}

// Snapshot 返回当前所有标记的只读视图（key -> remaining duration）。
// 供调试/admin 显示使用。
func (b *ChannelModelBlocklist) Snapshot() map[string]time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	out := make(map[string]time.Duration, len(b.blocked))
	for k, until := range b.blocked {
		if now.Before(until) {
			out[k.channelID+":"+k.model] = until.Sub(now)
		}
	}
	return out
}
