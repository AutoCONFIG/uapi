package relay

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrencyLimiter_BasicAcquireRelease(t *testing.T) {
	cl := NewConcurrencyLimiter(2)
	ctx := context.Background()

	if !cl.Acquire(ctx, "k") {
		t.Fatal("first acquire should succeed")
	}
	if !cl.Acquire(ctx, "k") {
		t.Fatal("second acquire should succeed")
	}
	if cl.ActiveCount("k") != 2 {
		t.Fatalf("active count should be 2, got %d", cl.ActiveCount("k"))
	}
	cl.Release("k")
	if cl.ActiveCount("k") != 1 {
		t.Fatalf("active count should be 1, got %d", cl.ActiveCount("k"))
	}
	cl.Release("k")
	if cl.ActiveCount("k") != 0 {
		t.Fatalf("active count should be 0, got %d", cl.ActiveCount("k"))
	}
}

func TestConcurrencyLimiter_QueueBlocksThenProceeds(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	// Take the only slot
	if !cl.Acquire(ctx, "k") {
		t.Fatal("acquire should succeed")
	}

	// Second acquire should block
	done := make(chan bool)
	go func() {
		ok := cl.Acquire(ctx, "k")
		done <- ok
	}()

	time.Sleep(50 * time.Millisecond)
	// Still blocked
	if cl.QueuedCount("k") != 1 {
		t.Fatalf("queue should have 1, got %d", cl.QueuedCount("k"))
	}

	// Release the slot
	cl.Release("k")

	// The waiter should proceed
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("queued acquire should succeed after release")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("queued acquire should not timeout after release")
	}
}

func TestConcurrencyLimiter_FIFOOrder(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "k")

	var order []string
	var mu sync.Mutex
	queued := make(chan struct{}) // each goroutine signals after entering queue

	// Start goroutine 1: will block in Acquire (queue position 0)
	go func() {
		<-queued // wait until we're in the queue
		cl.Acquire(ctx, "k")
		mu.Lock()
		order = append(order, "A")
		mu.Unlock()
		cl.Release("k")
	}()
	time.Sleep(10 * time.Millisecond)
	queued <- struct{}{} // let goroutine 1 enter Acquire and queue

	// Start goroutine 2: will block in Acquire (queue position 1)
	queued = make(chan struct{})
	go func() {
		<-queued
		cl.Acquire(ctx, "k")
		mu.Lock()
		order = append(order, "B")
		mu.Unlock()
		cl.Release("k")
	}()
	time.Sleep(10 * time.Millisecond)
	queued <- struct{}{}

	// Start goroutine 3: will block in Acquire (queue position 2)
	queued = make(chan struct{})
	go func() {
		<-queued
		cl.Acquire(ctx, "k")
		mu.Lock()
		order = append(order, "C")
		mu.Unlock()
		cl.Release("k")
	}()
	time.Sleep(10 * time.Millisecond)
	queued <- struct{}{}

	// Now release all 3 in order
	time.Sleep(20 * time.Millisecond) // ensure all goroutines are queued
	cl.Release("k")                    // wake A
	time.Sleep(20 * time.Millisecond)
	cl.Release("k")                    // wake B
	time.Sleep(20 * time.Millisecond)
	cl.Release("k")                    // wake C

	time.Sleep(50 * time.Millisecond) // let all finish

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 acquired, got %d", len(order))
	}
	if order[0] != "A" || order[1] != "B" || order[2] != "C" {
		t.Fatalf("FIFO violated: expected [A B C], got %v", order)
	}
}

func TestConcurrencyLimiter_ContextCancel(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "k")

	ctx2, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() {
		done <- cl.Acquire(ctx2, "k")
	}()

	time.Sleep(50 * time.Millisecond)
	if cl.QueuedCount("k") != 1 {
		t.Fatalf("queue should have 1, got %d", cl.QueuedCount("k"))
	}

	cancel() // cancel the waiting request

	select {
	case ok := <-done:
		if ok {
			t.Fatal("cancelled acquire should return false")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("cancelled acquire should return quickly")
	}

	if cl.QueuedCount("k") != 0 {
		t.Fatalf("queue should be empty after cancel, got %d", cl.QueuedCount("k"))
	}

	// Release the original slot — no one should wake up
	cl.Release("k")
	if cl.ActiveCount("k") != 0 {
		t.Fatalf("active count should be 0 after release, got %d", cl.ActiveCount("k"))
	}
}

func TestConcurrencyLimiter_CancelledWaiterSkipped(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "k")

	// First waiter: will be cancelled
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		cl.Acquire(ctx1, "k")
	}()
	time.Sleep(30 * time.Millisecond)
	cancel1()
	time.Sleep(30 * time.Millisecond)

	// Second waiter: should proceed after release
	done := make(chan bool)
	go func() {
		done <- cl.Acquire(ctx, "k")
	}()
	time.Sleep(30 * time.Millisecond)

	cl.Release("k")

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("second waiter should succeed")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("second waiter should not timeout")
	}

	cl.Release("k")
}

func TestConcurrencyLimiter_Unlimited(t *testing.T) {
	cl := NewConcurrencyLimiter(0) // unlimited
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		if !cl.Acquire(ctx, "k") {
			t.Fatalf("unlimited acquire %d should succeed", i)
		}
	}
	for i := 0; i < 100; i++ {
		cl.Release("k")
	}
}

func TestConcurrencyLimiter_DifferentKeys(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "a")
	cl.Acquire(ctx, "b") // different key, should not block

	if cl.ActiveCount("a") != 1 || cl.ActiveCount("b") != 1 {
		t.Fatal("different keys should have independent counts")
	}
	cl.Release("a")
	cl.Release("b")
}

func TestConcurrencyLimiter_ConcurrentRelease(t *testing.T) {
	cl := NewConcurrencyLimiter(3)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl.Acquire(ctx, "k")
		}()
	}
	wg.Wait()

	// Concurrent releases
	var wg2 sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			cl.Release("k")
		}()
	}
	wg2.Wait()

	if cl.ActiveCount("k") != 0 {
		t.Fatalf("active count should be 0 after all releases, got %d", cl.ActiveCount("k"))
	}
}

func TestConcurrencyLimiter_ReleaseWithoutAcquire(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	// Release without acquire should not panic
	cl.Release("nonexistent")
	if cl.ActiveCount("nonexistent") != 0 {
		t.Fatal("release without acquire should not create negative count")
	}
}

func TestConcurrencyLimiter_QueueStats(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "k")

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl.Acquire(ctx, "k")
		}()
	}

	time.Sleep(50 * time.Millisecond)
	if cl.QueuedCount("k") != 5 {
		t.Fatalf("queue count should be 5, got %d", cl.QueuedCount("k"))
	}
	if cl.ActiveCount("k") != 1 {
		t.Fatalf("active count should be 1, got %d", cl.ActiveCount("k"))
	}

	// Release all
	for i := 0; i < 6; i++ {
		cl.Release("k")
	}

	wg.Wait()
	if cl.QueuedCount("k") != 0 {
		t.Fatalf("queue should be empty, got %d", cl.QueuedCount("k"))
	}
}

func TestConcurrencyLimiter_RaceDetection(t *testing.T) {
	cl := NewConcurrencyLimiter(3)
	ctx := context.Background()

	var wg sync.WaitGroup
	const n = 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k"
			if cl.Acquire(ctx, key) {
				time.Sleep(time.Millisecond)
				cl.Release(key)
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrencyLimiter_CancelRace(t *testing.T) {
	cl := NewConcurrencyLimiter(1)
	ctx := context.Background()

	cl.Acquire(ctx, "k")

	var cancelCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx2, cancel := context.WithCancel(context.Background())
			go func() {
				cl.Acquire(ctx2, "k")
			}()
			time.Sleep(time.Millisecond)
			cancel()
			cancelCount.Add(1)
		}()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// All cancelled, queue should be clean
	if cl.QueuedCount("k") != 0 {
		t.Fatalf("queue should be empty after all cancels, got %d", cl.QueuedCount("k"))
	}
	cl.Release("k")
}
