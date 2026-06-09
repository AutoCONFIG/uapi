package relay

import (
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// SetIfAbsent vs ForceSet 语义
// ---------------------------------------------------------------------------

func TestSetIfAbsent_PreservesExistingEntry(t *testing.T) {
	c := NewAffinityCache()
	c.SetIfAbsent("t", "m", "s", "ch-first", "acc-first", 60)

	// Second SetIfAbsent with different channel/account must NOT overwrite
	ok := c.SetIfAbsent("t", "m", "s", "ch-second", "acc-second", 60)
	if ok {
		t.Fatal("SetIfAbsent should return false when entry already exists")
	}
	ch, acc := c.Get("t", "m", "s")
	if ch != "ch-first" || acc != "acc-first" {
		t.Fatalf("expected original binding ch-first/acc-first, got %s/%s", ch, acc)
	}
}

func TestSetIfAbsent_SetsWhenEmpty(t *testing.T) {
	c := NewAffinityCache()
	ok := c.SetIfAbsent("t", "m", "s", "ch1", "acc1", 60)
	if !ok {
		t.Fatal("SetIfAbsent should return true when no entry exists")
	}
	ch, acc := c.Get("t", "m", "s")
	if ch != "ch1" || acc != "acc1" {
		t.Fatalf("got %s/%s", ch, acc)
	}
}

func TestSetIfAbsent_SetsAfterExpiry(t *testing.T) {
	c := NewAffinityCache()
	// TTL=0 means not set, use TTL=1 then wait
	c.ForceSet("t", "m", "s", "ch-old", "acc-old", 1)
	time.Sleep(1100 * time.Millisecond)

	ok := c.SetIfAbsent("t", "m", "s", "ch-new", "acc-new", 60)
	if !ok {
		t.Fatal("SetIfAbsent should succeed after old entry expired")
	}
	ch, acc := c.Get("t", "m", "s")
	if ch != "ch-new" || acc != "acc-new" {
		t.Fatalf("got %s/%s", ch, acc)
	}
}

func TestSetIfAbsent_RejectsInvalidFields(t *testing.T) {
	c := NewAffinityCache()
	// Empty scope is valid (unscoped affinity)
	if c.SetIfAbsent("t", "m", "", "ch", "acc", 60) != true {
		t.Fatal("empty scope should be accepted")
	}
	c.Clear()

	if c.SetIfAbsent("t", "m", "s", "", "acc", 60) {
		t.Fatal("should reject empty channelID")
	}
	if c.SetIfAbsent("t", "m", "s", "ch", "", 60) {
		t.Fatal("should reject empty accountID")
	}
	if c.SetIfAbsent("t", "m", "s", "ch", "acc", 0) {
		t.Fatal("should reject ttl <= 0")
	}
	if c.SetIfAbsent("t", "m", "s", "ch", "acc", -1) {
		t.Fatal("should reject negative ttl")
	}
}

func TestForceSet_OverwritesExisting(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch-old", "acc-old", 60)
	c.ForceSet("t", "m", "s", "ch-new", "acc-new", 60)

	ch, acc := c.Get("t", "m", "s")
	if ch != "ch-new" || acc != "acc-new" {
		t.Fatalf("ForceSet should overwrite, got %s/%s", ch, acc)
	}
	// Old channel must NOT appear in reverse index
	keys, ok := c.byChannel["ch-old"]
	if ok && len(keys) > 0 {
		t.Fatal("old channel reverse index should be cleaned after ForceSet overwrite")
	}
}

func TestSet_BackwardCompat(t *testing.T) {
	c := NewAffinityCache()
	// Set should behave like ForceSet
	c.Set("t", "m", "", "ch1", "acc1", 60)
	c.Set("t", "m", "", "ch2", "acc2", 60)
	ch, acc := c.Get("t", "m", "")
	if ch != "ch2" || acc != "acc2" {
		t.Fatalf("Set should overwrite like ForceSet, got %s/%s", ch, acc)
	}
}

// ---------------------------------------------------------------------------
// 反向索引一致性
// ---------------------------------------------------------------------------

func TestReverseIndex_MaintainedOnSet(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 60)
	c.ForceSet("t2", "m", "s2", "chA", "acc2", 60)
	c.ForceSet("t3", "m", "s3", "chB", "acc1", 60)

	if len(c.byChannel["chA"]) != 2 {
		t.Fatalf("chA should have 2 entries, got %d", len(c.byChannel["chA"]))
	}
	if len(c.byChannel["chB"]) != 1 {
		t.Fatalf("chB should have 1 entry, got %d", len(c.byChannel["chB"]))
	}
	if len(c.byAccount["acc1"]) != 2 {
		t.Fatalf("acc1 should have 2 entries, got %d", len(c.byAccount["acc1"]))
	}
	if len(c.byAccount["acc2"]) != 1 {
		t.Fatalf("acc2 should have 1 entry, got %d", len(c.byAccount["acc2"]))
	}
}

func TestReverseIndex_CleanedOnEvictChannel(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 60)
	c.ForceSet("t2", "m", "s2", "chA", "acc2", 60)
	c.ForceSet("t3", "m", "s3", "chB", "acc1", 60)

	c.EvictChannel("chA")

	// chA entries gone
	if _, ok := c.byChannel["chA"]; ok {
		t.Fatal("chA reverse index should be removed after EvictChannel")
	}
	// acc2 should have no entries left (only was on chA)
	if _, ok := c.byAccount["acc2"]; ok {
		t.Fatal("acc2 reverse index should be removed after its only entry was evicted")
	}
	// acc1 still has chB entry
	if len(c.byAccount["acc1"]) != 1 {
		t.Fatalf("acc1 should still have 1 entry (on chB), got %d", len(c.byAccount["acc1"]))
	}
	// chB entry preserved
	ch, acc := c.Get("t3", "m", "s3")
	if ch != "chB" || acc != "acc1" {
		t.Fatalf("chB entry should be preserved, got %s/%s", ch, acc)
	}
}

func TestReverseIndex_CleanedOnEvictAccount(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 60)
	c.ForceSet("t2", "m", "s2", "chA", "acc2", 60)
	c.ForceSet("t3", "m", "s3", "chB", "acc1", 60)

	// EvictAccount removes ALL entries for acc1 (across chA and chB)
	c.EvictAccount("acc1")

	if _, ok := c.byAccount["acc1"]; ok {
		t.Fatal("acc1 reverse index should be removed after EvictAccount")
	}
	// chA should still have acc2 entry
	if len(c.byChannel["chA"]) != 1 {
		t.Fatalf("chA should still have 1 entry, got %d", len(c.byChannel["chA"]))
	}
	// chB should be empty
	if _, ok := c.byChannel["chB"]; ok {
		t.Fatal("chB reverse index should be removed after its only entry was evicted")
	}
}

// ---------------------------------------------------------------------------
// EvictChannel / EvictAccount 边界
// ---------------------------------------------------------------------------

func TestEvictChannel_EmptyChannelID(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.EvictChannel("") // no-op
	ch, _ := c.Get("t", "m", "s")
	if ch != "ch" {
		t.Fatal("EvictChannel('') should be a no-op")
	}
}

func TestEvictAccount_EmptyAccountID(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.EvictAccount("") // no-op
	ch, _ := c.Get("t", "m", "s")
	if ch != "ch" {
		t.Fatal("EvictAccount('') should be a no-op")
	}
}

func TestEvictChannel_NonExistent(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.EvictChannel("ch-nonexistent") // no-op
	ch, _ := c.Get("t", "m", "s")
	if ch != "ch" {
		t.Fatal("EvictChannel for nonexistent channel should not affect other entries")
	}
}

func TestEvictAccount_NonExistent(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.EvictAccount("acc-nonexistent")
	ch, _ := c.Get("t", "m", "s")
	if ch != "ch" {
		t.Fatal("EvictAccount for nonexistent account should not affect other entries")
	}
}

func TestInvalidateChannel_Works(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.InvalidateChannel("ch")
	ch, _ := c.Get("t", "m", "s")
	if ch != "" {
		t.Fatal("InvalidateChannel should remove entries")
	}
}

func TestInvalidateAccount_Works(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	c.InvalidateAccount("acc")
	ch, _ := c.Get("t", "m", "s")
	if ch != "" {
		t.Fatal("InvalidateAccount should remove entries")
	}
}

// ---------------------------------------------------------------------------
// Clear / Snapshot / Len
// ---------------------------------------------------------------------------

func TestClear_ResetsEverything(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 60)
	c.ForceSet("t2", "m", "s2", "chB", "acc2", 60)

	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("after Clear, Len should be 0, got %d", c.Len())
	}
	if len(c.byChannel) != 0 || len(c.byAccount) != 0 {
		t.Fatal("reverse indexes should be empty after Clear")
	}
	ch, _ := c.Get("t1", "m", "s1")
	if ch != "" {
		t.Fatal("entries should be gone after Clear")
	}
}

func TestSnapshot_ReturnsOnlyValidEntries(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 1)
	c.ForceSet("t2", "m", "s2", "chB", "acc2", 60)
	time.Sleep(1100 * time.Millisecond)

	snap := c.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot should have 1 valid entry, got %d", len(snap))
	}
	if _, ok := snap[c.key("t2", "m", "s2")]; !ok {
		t.Fatal("Snapshot should contain the non-expired entry")
	}
}

func TestLen_TracksEntries(t *testing.T) {
	c := NewAffinityCache()
	if c.Len() != 0 {
		t.Fatalf("empty cache Len should be 0, got %d", c.Len())
	}
	c.ForceSet("t1", "m", "s1", "chA", "acc1", 60)
	c.ForceSet("t2", "m", "s2", "chB", "acc2", 60)
	if c.Len() != 2 {
		t.Fatalf("Len should be 2, got %d", c.Len())
	}
	c.EvictChannel("chA")
	if c.Len() != 1 {
		t.Fatalf("Len should be 1 after evict, got %d", c.Len())
	}
}

// ---------------------------------------------------------------------------
// Get Hit/Miss + 过期惰性清理
// ---------------------------------------------------------------------------

func TestGetHit_ReportsTrueOnValidEntry(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 60)
	ch, acc, hit := c.GetHit("t", "m", "s")
	if !hit || ch != "ch" || acc != "acc" {
		t.Fatalf("expected hit ch/acc, got %s/%s/%v", ch, acc, hit)
	}
}

func TestGetHit_ReportsFalseOnMiss(t *testing.T) {
	c := NewAffinityCache()
	ch, acc, hit := c.GetHit("t", "m", "s")
	if hit || ch != "" || acc != "" {
		t.Fatalf("expected miss, got %s/%s/%v", ch, acc, hit)
	}
}

func TestGetHit_ReportsFalseAfterExpiry(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 1)
	time.Sleep(1100 * time.Millisecond)
	ch, acc, hit := c.GetHit("t", "m", "s")
	if hit || ch != "" || acc != "" {
		t.Fatalf("expected miss after expiry, got %s/%s/%v", ch, acc, hit)
	}
}

func TestGet_LazyExpiry_CleansReverseIndex(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "s", "ch", "acc", 1)
	time.Sleep(1100 * time.Millisecond)

	// Get triggers lazy delete
	ch, _ := c.Get("t", "m", "s")
	if ch != "" {
		t.Fatal("Get should return empty after expiry")
	}
	// Reverse indexes should be cleaned
	if _, ok := c.byChannel["ch"]; ok {
		t.Fatal("byChannel should be cleaned after lazy expiry")
	}
	if _, ok := c.byAccount["acc"]; ok {
		t.Fatal("byAccount should be cleaned after lazy expiry")
	}
}

// ---------------------------------------------------------------------------
// 无 scope 的 key 格式
// ---------------------------------------------------------------------------

func TestKeyFormat_WithAndWithoutScope(t *testing.T) {
	c := NewAffinityCache()
	c.ForceSet("t", "m", "", "ch1", "acc1", 60)
	c.ForceSet("t", "m", "session-1", "ch2", "acc2", 60)

	// Without scope
	ch, acc := c.Get("t", "m", "")
	if ch != "ch1" || acc != "acc1" {
		t.Fatalf("no-scope entry: got %s/%s", ch, acc)
	}
	// With scope
	ch, acc = c.Get("t", "m", "session-1")
	if ch != "ch2" || acc != "acc2" {
		t.Fatalf("scoped entry: got %s/%s", ch, acc)
	}
	// They must not collide
	if c.Len() != 2 {
		t.Fatalf("scoped and unscoped should be separate entries, Len=%d", c.Len())
	}
}

// ---------------------------------------------------------------------------
// 并发安全
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	c := NewAffinityCache()
	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent writers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch := "ch" + string(rune('A'+i%5))
			acc := "acc" + string(rune('0'+i%3))
			c.ForceSet("t", "m", string(rune('a'+i%10)), ch, acc, 60)
		}(i)
	}

	// Concurrent readers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Get("t", "m", string(rune('a'+i%10)))
			c.GetHit("t", "m", string(rune('a'+i%10)))
		}(i)
	}

	// Concurrent evictors
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.EvictChannel("ch" + string(rune('A'+i)))
		}(i)
	}

	// Concurrent Len/Snapshot
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Len()
			c.Snapshot()
		}()
	}

	wg.Wait()
	// No race detector failure = pass
}

func TestConcurrentSetIfAbsent_NoDoubleWrite(t *testing.T) {
	c := NewAffinityCache()
	var wg sync.WaitGroup
	const goroutines = 100

	successCount := 0
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.SetIfAbsent("t", "m", "s", "ch", "acc", 60) {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("SetIfAbsent should succeed exactly once, succeeded %d times", successCount)
	}
	ch, acc := c.Get("t", "m", "s")
	if ch != "ch" || acc != "acc" {
		t.Fatalf("final value should be ch/acc, got %s/%s", ch, acc)
	}
}
