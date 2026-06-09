package relay

import (
	"context"
	"sync"
)

// waiter represents a goroutine waiting for a concurrency slot.
type waiter struct {
	ch      chan struct{} // buffered(1): Release or Cancel sends here
	key     string
	cancelled bool
}

// ConcurrencyLimiter enforces per-key concurrent request limits with FIFO queuing.
// When the limit is reached, new requests block in a queue until a slot opens.
// Queued requests can be cancelled via context (e.g., client disconnect).
type ConcurrencyLimiter struct {
	mu      sync.Mutex
	counts  map[string]int    // key → active count
	queues  map[string][]*waiter // key → FIFO wait queue
	limit   int
}

func NewConcurrencyLimiter(limit int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		counts: make(map[string]int),
		queues: make(map[string][]*waiter),
		limit:  limit,
	}
}

// Acquire reserves a slot for the given key, blocking if at capacity.
// Returns true when a slot is acquired, false if the context is cancelled while waiting.
func (cl *ConcurrencyLimiter) Acquire(ctx context.Context, key string) bool {
	return cl.AcquireWithLimit(ctx, key, cl.limit)
}

// AcquireWithLimit reserves a slot using a per-call limit.
func (cl *ConcurrencyLimiter) AcquireWithLimit(ctx context.Context, key string, limit int) bool {
	if limit <= 0 {
		return true
	}

	cl.mu.Lock()
	if cl.counts[key] < limit {
		cl.counts[key]++
		cl.mu.Unlock()
		return true
	}
	// At capacity: enqueue and wait
	w := &waiter{ch: make(chan struct{}, 1), key: key}
	cl.queues[key] = append(cl.queues[key], w)
	cl.mu.Unlock()

	// Block until signaled (Release) or context cancelled
	select {
	case <-w.ch:
		// Woken by Release — slot acquired
		return true
	case <-ctx.Done():
		// Cancelled — remove from queue if still enqueued
		cl.mu.Lock()
		if !w.cancelled {
			w.cancelled = true
			cl.removeWaiterLocked(key, w)
		}
		cl.mu.Unlock()
		return false
	}
}

// Release frees a slot for the given key and wakes the oldest waiter if any.
func (cl *ConcurrencyLimiter) Release(key string) {
	cl.mu.Lock()
	if cl.counts[key] > 0 {
		cl.counts[key]--
	}
	// Try to wake the oldest non-cancelled waiter
	var toSignal *waiter
	q := cl.queues[key]
	for len(q) > 0 {
		if q[0].cancelled {
			q = q[1:]
			continue
		}
		toSignal = q[0]
		cl.queues[key] = q[1:]
		break
	}
	if len(cl.queues[key]) == 0 {
		delete(cl.queues, key)
	}
	if cl.counts[key] <= 0 {
		delete(cl.counts, key)
	}
	cl.mu.Unlock()

	// Signal outside lock to avoid deadlock
	if toSignal != nil {
		toSignal.ch <- struct{}{}
	}
}

// removeWaiterLocked removes a waiter from its queue. Caller must hold cl.mu.
func (cl *ConcurrencyLimiter) removeWaiterLocked(key string, w *waiter) {
	q := cl.queues[key]
	for i, entry := range q {
		if entry == w {
			cl.queues[key] = append(q[:i], q[i+1:]...)
			if len(cl.queues[key]) == 0 {
				delete(cl.queues, key)
			}
			return
		}
	}
}

// QueuedCount returns the number of requests waiting in the queue for a key.
func (cl *ConcurrencyLimiter) QueuedCount(key string) int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return len(cl.queues[key])
}

// ActiveCount returns the number of active (in-progress) requests for a key.
func (cl *ConcurrencyLimiter) ActiveCount(key string) int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.counts[key]
}

// PerTokenStats returns active and queued counts for a specific token.
func (cl *ConcurrencyLimiter) PerTokenStats(tokenID string) (active int, queued int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.counts[tokenID], len(cl.queues[tokenID])
}
