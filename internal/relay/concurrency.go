package relay

import (
	"sync"
)

// ConcurrencyLimiter enforces per-key concurrent request limits.
type ConcurrencyLimiter struct {
	mu     sync.Mutex
	counts map[string]int // tokenID → active count
	limit  int
}

func NewConcurrencyLimiter(limit int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		counts: make(map[string]int),
		limit:  limit,
	}
}

// Acquire reserves a slot for the given key. Returns false if at capacity.
func (cl *ConcurrencyLimiter) Acquire(key string) bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.limit <= 0 {
		return true
	}
	if cl.counts[key] >= cl.limit {
		return false
	}
	cl.counts[key]++
	return true
}

// Release frees a slot for the given key.
func (cl *ConcurrencyLimiter) Release(key string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.counts[key]--
	if cl.counts[key] <= 0 {
		delete(cl.counts, key)
	}
}
