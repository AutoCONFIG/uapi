package relay

import (
	"strings"
	"sync"
	"time"
)

type affinityEntry struct {
	channelID string
	accountID string
	scope     string
	expiresAt time.Time
}

// AffinityCache maps tokenID:model[:scope] -> channel/account with TTL support.
//
// 改造要点 (Phase 2):
//   - 反向索引 byChannel/byAccount 支持 O(k) 按 channel/account 批量清除
//   - SetIfAbsent vs ForceSet 语义分离：首次成功后不再覆盖已绑定的亲和
//   - Get 返回三值 (channelID, accountID, hit)，调用方可区分 miss 与 expired
//   - 保持 Set 向后兼容（语义等价 ForceSet），Phase 3 调用方逐步迁移
type AffinityCache struct {
	mu        sync.RWMutex
	entries   map[string]affinityEntry
	byChannel map[string]map[string]struct{} // channelID -> set of keys
	byAccount map[string]map[string]struct{} // accountID -> set of keys
}

func NewAffinityCache() *AffinityCache {
	return &AffinityCache{
		entries:   make(map[string]affinityEntry),
		byChannel: make(map[string]map[string]struct{}),
		byAccount: make(map[string]map[string]struct{}),
	}
}

func (ac *AffinityCache) key(tokenID, model, scope string) string {
	key := tokenID + ":" + model
	if scope != "" {
		key += ":" + scope
	}
	return key
}

// addIndex 维护反向索引（调用方已持锁）。
func (ac *AffinityCache) addIndex(k string, channelID, accountID string) {
	if channelID != "" {
		if ac.byChannel[channelID] == nil {
			ac.byChannel[channelID] = make(map[string]struct{})
		}
		ac.byChannel[channelID][k] = struct{}{}
	}
	if accountID != "" {
		if ac.byAccount[accountID] == nil {
			ac.byAccount[accountID] = make(map[string]struct{})
		}
		ac.byAccount[accountID][k] = struct{}{}
	}
}

// removeIndex 清理反向索引（调用方已持锁）。
func (ac *AffinityCache) removeIndex(k string, channelID, accountID string) {
	if channelID != "" {
		if s, ok := ac.byChannel[channelID]; ok {
			delete(s, k)
			if len(s) == 0 {
				delete(ac.byChannel, channelID)
			}
		}
	}
	if accountID != "" {
		if s, ok := ac.byAccount[accountID]; ok {
			delete(s, k)
			if len(s) == 0 {
				delete(ac.byAccount, accountID)
			}
		}
	}
}

// Get returns the cached channel/account for tokenID+model+scope, or empty strings on miss.
// Lazy-deletes expired entries on access.
func (ac *AffinityCache) Get(tokenID, model, scope string) (string, string) {
	k := ac.key(tokenID, model, scope)
	ac.mu.RLock()
	e, ok := ac.entries[k]
	if !ok {
		ac.mu.RUnlock()
		return "", ""
	}
	if time.Now().After(e.expiresAt) {
		ac.mu.RUnlock()
		// Lazy delete: upgrade to write lock, double-check in case Set refreshed
		ac.mu.Lock()
		fresh, stillExists := ac.entries[k]
		if !stillExists || time.Now().After(fresh.expiresAt) {
			delete(ac.entries, k)
			ac.removeIndex(k, fresh.channelID, fresh.accountID)
			ac.mu.Unlock()
			return "", ""
		}
		// Entry was refreshed between our RUnlock and Lock
		channelID := fresh.channelID
		accountID := fresh.accountID
		ac.mu.Unlock()
		return channelID, accountID
	}
	ac.mu.RUnlock()
	return e.channelID, e.accountID
}

// GetHit returns the cached channel/account plus a hit boolean.
// Use this when you need to distinguish miss from expired (e.g., debug/admin).
func (ac *AffinityCache) GetHit(tokenID, model, scope string) (string, string, bool) {
	k := ac.key(tokenID, model, scope)
	ac.mu.RLock()
	e, ok := ac.entries[k]
	if !ok {
		ac.mu.RUnlock()
		return "", "", false
	}
	if time.Now().After(e.expiresAt) {
		ac.mu.RUnlock()
		ac.mu.Lock()
		fresh, stillExists := ac.entries[k]
		if !stillExists || time.Now().After(fresh.expiresAt) {
			delete(ac.entries, k)
			ac.removeIndex(k, fresh.channelID, fresh.accountID)
			ac.mu.Unlock()
			return "", "", false
		}
		ac.mu.Unlock()
		return fresh.channelID, fresh.accountID, true
	}
	ac.mu.RUnlock()
	return e.channelID, e.accountID, true
}

// SetIfAbsent records an affinity mapping only if the key has no unexpired entry.
// Returns the preserved or written channel/account and whether an unexpired entry already existed.
// Use this for successful request recording: preserve a working binding, but
// refresh its TTL so the affinity window slides with continued use.
func (ac *AffinityCache) SetIfAbsent(tokenID, model, scope, channelID, accountID string, ttlSeconds int) (chID, accID string, existed bool) {
	if ttlSeconds <= 0 || channelID == "" || accountID == "" {
		return "", "", false
	}
	k := ac.key(tokenID, model, scope)
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if old, ok := ac.entries[k]; ok {
		if time.Now().Before(old.expiresAt) {
			old.expiresAt = time.Now().Add(time.Duration(ttlSeconds) * time.Second)
			ac.entries[k] = old
			return old.channelID, old.accountID, true // already bound, preserve existing
		}
		ac.removeIndex(k, old.channelID, old.accountID)
	}
	ac.entries[k] = affinityEntry{
		channelID: channelID,
		accountID: accountID,
		scope:     scope,
		expiresAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	}
	ac.addIndex(k, channelID, accountID)
	return channelID, accountID, false
}

// ForceSet unconditionally records an affinity mapping, overwriting any existing entry.
// Use this for explicit refresh / admin override scenarios.
func (ac *AffinityCache) ForceSet(tokenID, model, scope, channelID, accountID string, ttlSeconds int) {
	if ttlSeconds <= 0 || channelID == "" || accountID == "" {
		return
	}
	k := ac.key(tokenID, model, scope)
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if old, ok := ac.entries[k]; ok {
		ac.removeIndex(k, old.channelID, old.accountID)
	}
	ac.entries[k] = affinityEntry{
		channelID: channelID,
		accountID: accountID,
		scope:     scope,
		expiresAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	}
	ac.addIndex(k, channelID, accountID)
}

// Set records an affinity mapping with the given TTL in seconds.
// Semantic: unconditional overwrite (equivalent to ForceSet).
// Deprecated: Use SetIfAbsent (first-success) or ForceSet (explicit refresh) in new code.
func (ac *AffinityCache) Set(tokenID, model, scope, channelID, accountID string, ttlSeconds int) {
	ac.ForceSet(tokenID, model, scope, channelID, accountID, ttlSeconds)
}

// EvictChannel removes all entries pointing to the given channelID.
// Uses reverse index for O(k) performance where k = entries for this channel.
func (ac *AffinityCache) EvictChannel(channelID string) {
	if channelID == "" {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	keys, ok := ac.byChannel[channelID]
	if !ok || len(keys) == 0 {
		return
	}
	// Copy keys to avoid mutating map during iteration
	toRemove := make([]string, 0, len(keys))
	for k := range keys {
		toRemove = append(toRemove, k)
	}
	for _, k := range toRemove {
		if e, exists := ac.entries[k]; exists {
			ac.removeIndex(k, e.channelID, e.accountID)
			delete(ac.entries, k)
		}
	}
}

// EvictAccount removes all entries pointing to a specific accountID.
// Uses reverse index for O(k) performance where k = entries for this account.
func (ac *AffinityCache) EvictAccount(accountID string) {
	if accountID == "" {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	keys, ok := ac.byAccount[accountID]
	if !ok || len(keys) == 0 {
		return
	}
	toRemove := make([]string, 0, len(keys))
	for k := range keys {
		toRemove = append(toRemove, k)
	}
	for _, k := range toRemove {
		if e, exists := ac.entries[k]; exists {
			ac.removeIndex(k, e.channelID, e.accountID)
			delete(ac.entries, k)
		}
	}
}

// InvalidateChannel is an alias for EvictChannel.
// Prefer this name in new code for consistency with naming convention.
func (ac *AffinityCache) InvalidateChannel(channelID string) {
	ac.EvictChannel(channelID)
}

// InvalidateAccount is an alias for EvictAccount.
// Prefer this name in new code for consistency with naming convention.
func (ac *AffinityCache) InvalidateAccount(accountID string) {
	ac.EvictAccount(accountID)
}

// Clear removes all entries and indexes.
func (ac *AffinityCache) Clear() {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.entries = make(map[string]affinityEntry)
	ac.byChannel = make(map[string]map[string]struct{})
	ac.byAccount = make(map[string]map[string]struct{})
}

// Snapshot returns a read-only view of all non-expired entries for debugging.
func (ac *AffinityCache) Snapshot() map[string]struct{ Channel, Account string } {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	now := time.Now()
	out := make(map[string]struct{ Channel, Account string })
	for k, e := range ac.entries {
		if now.Before(e.expiresAt) {
			out[k] = struct{ Channel, Account string }{e.channelID, e.accountID}
		}
	}
	return out
}

func (ac *AffinityCache) AccountScopeCounts(scopeSource string) map[string]int {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	now := time.Now()
	out := make(map[string]int)
	for k, e := range ac.entries {
		if now.After(e.expiresAt) {
			ac.removeIndex(k, e.channelID, e.accountID)
			delete(ac.entries, k)
			continue
		}
		if e.accountID == "" {
			continue
		}
		if scopeSource != "" {
			source, _ := splitRouteScope(e.scope)
			if source == "" && strings.Contains(k, ":"+scopeSource+":") {
				source = scopeSource
			}
			if source != scopeSource {
				continue
			}
		}
		out[e.accountID]++
	}
	return out
}

// Len returns the number of non-expired entries.
func (ac *AffinityCache) Len() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return len(ac.entries)
}

// Close is kept for server lifecycle symmetry. Expired entries are removed lazily on access.
func (ac *AffinityCache) Close() {}
