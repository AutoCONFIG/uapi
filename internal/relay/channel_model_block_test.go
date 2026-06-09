package relay

import (
	"sync"
	"testing"
	"time"
)

func TestBlocklist_BlockAndCheck(t *testing.T) {
	b := NewChannelModelBlocklist(1 * time.Second)
	b.Block("ch-1", "gpt-4")
	if !b.IsBlocked("ch-1", "gpt-4") {
		t.Error("should be blocked")
	}
	if b.IsBlocked("ch-1", "gpt-5") {
		t.Error("different model should not be blocked")
	}
	if b.IsBlocked("ch-2", "gpt-4") {
		t.Error("different channel should not be blocked")
	}
}

func TestBlocklist_Expiry(t *testing.T) {
	b := NewChannelModelBlocklist(50 * time.Millisecond)
	b.Block("ch-1", "gpt-4")
	if !b.IsBlocked("ch-1", "gpt-4") {
		t.Error("should be blocked initially")
	}
	time.Sleep(100 * time.Millisecond)
	if b.IsBlocked("ch-1", "gpt-4") {
		t.Error("should be expired")
	}
	// 过期后 entry 应被主动清理
	snap := b.Snapshot()
	if len(snap) != 0 {
		t.Errorf("expired entry should be removed, got %d", len(snap))
	}
}

func TestBlocklist_ClearChannel(t *testing.T) {
	b := NewChannelModelBlocklist(1 * time.Minute)
	b.Block("ch-1", "gpt-4")
	b.Block("ch-1", "gpt-5")
	b.Block("ch-2", "gpt-4")

	cleared := b.ClearChannel("ch-1")
	if cleared != 2 {
		t.Errorf("cleared should be 2, got %d", cleared)
	}
	if b.IsBlocked("ch-1", "gpt-4") || b.IsBlocked("ch-1", "gpt-5") {
		t.Error("ch-1 should be cleared")
	}
	if !b.IsBlocked("ch-2", "gpt-4") {
		t.Error("ch-2 should remain blocked")
	}
}

func TestBlocklist_EmptyArgs(t *testing.T) {
	b := NewChannelModelBlocklist(0) // 默认 TTL
	b.Block("", "gpt-4")
	b.Block("ch-1", "")
	if b.IsBlocked("", "gpt-4") || b.IsBlocked("ch-1", "") {
		t.Error("empty args should be ignored")
	}
}

func TestBlocklist_Snapshot(t *testing.T) {
	b := NewChannelModelBlocklist(1 * time.Minute)
	b.Block("ch-1", "gpt-4")
	b.Block("ch-2", "gpt-5")

	snap := b.Snapshot()
	if len(snap) != 2 {
		t.Errorf("snapshot size: want 2, got %d", len(snap))
	}
	if _, ok := snap["ch-1:gpt-4"]; !ok {
		t.Error("ch-1:gpt-4 missing")
	}
	if _, ok := snap["ch-2:gpt-5"]; !ok {
		t.Error("ch-2:gpt-5 missing")
	}
}

func TestBlocklist_ClearAll(t *testing.T) {
	b := NewChannelModelBlocklist(1 * time.Minute)
	b.Block("ch-1", "gpt-4")
	b.Block("ch-2", "gpt-5")
	b.ClearAll()
	if len(b.Snapshot()) != 0 {
		t.Error("ClearAll should remove all entries")
	}
}

func TestBlocklist_Concurrent(t *testing.T) {
	b := NewChannelModelBlocklist(1 * time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			b.Block("ch-shared", "gpt-4")
			b.IsBlocked("ch-shared", "gpt-4")
			if id%10 == 0 {
				b.ClearChannel("ch-shared")
			}
		}(i)
	}
	wg.Wait()
}

func TestBlocklist_DefaultTTL(t *testing.T) {
	b := NewChannelModelBlocklist(-1)
	if b.ttl != DefaultModelBlockTTL {
		t.Errorf("default ttl: want %v, got %v", DefaultModelBlockTTL, b.ttl)
	}
}
