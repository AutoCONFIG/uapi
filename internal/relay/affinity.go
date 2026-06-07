package relay

import (
	"sync"
	"time"
)

type affinityEntry struct {
	channelID string
	accountID string
	expiresAt time.Time
}

// AffinityCache maps tokenID:model[:session] -> channel/account with TTL support.
type AffinityCache struct {
	mu      sync.RWMutex
	entries map[string]affinityEntry
}

func NewAffinityCache() *AffinityCache {
	return &AffinityCache{
		entries: make(map[string]affinityEntry),
	}
}

func (ac *AffinityCache) key(tokenID, model, scope string) string {
	key := tokenID + ":" + model
	if scope != "" {
		key += ":" + scope
	}
	return key
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

// Set records an affinity mapping with the given TTL in seconds.
func (ac *AffinityCache) Set(tokenID, model, scope, channelID, accountID string, ttlSeconds int) {
	if ttlSeconds <= 0 {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.entries[ac.key(tokenID, model, scope)] = affinityEntry{
		channelID: channelID,
		accountID: accountID,
		expiresAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	}
}

// EvictChannel removes all entries pointing to the given channelID.
func (ac *AffinityCache) EvictChannel(channelID string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for k, v := range ac.entries {
		if v.channelID == channelID {
			delete(ac.entries, k)
		}
	}
}

// Close is kept for server lifecycle symmetry. Expired entries are removed lazily on access.
func (ac *AffinityCache) Close() {}
