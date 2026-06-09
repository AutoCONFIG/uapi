package relay

import (
	"sync"
	"testing"
	"time"
)

func TestCooldownPolicy_NoCooldownForNonAccountSide(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	classes := []ErrorClass{ErrServerSide, ErrConfigSide, ErrClientSide, ErrAccountTerminal, ErrUnknown}
	for _, c := range classes {
		got := p.ComputeCooldown(c, 500, "acc-1")
		if got != 0 {
			t.Errorf("class %s should return 0, got %v", c, got)
		}
	}
}

func TestCooldownPolicy_StatusCodeMapping(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	cases := []struct {
		code int
		want time.Duration
	}{
		{401, 1 * time.Minute},
		{402, 5 * time.Minute},
		{403, 15 * time.Minute},
		{418, 1 * time.Minute}, // 未知状态码兜底 1min
	}
	for _, c := range cases {
		got := p.ComputeCooldown(ErrAccountSide, c.code, "acc-static")
		if got != c.want {
			t.Errorf("code %d: want %v, got %v", c.code, c.want, got)
		}
	}
}

func TestCooldownPolicy_429ExponentialBackoff(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	want := []time.Duration{
		10 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
	}
	for i, expected := range want {
		got := p.ComputeCooldown(ErrAccountSide, 429, "acc-429")
		if got != expected {
			t.Errorf("step %d: want %v, got %v", i, expected, got)
		}
	}

	// 超过序列后封顶在最后一级
	for i := 0; i < 3; i++ {
		got := p.ComputeCooldown(ErrAccountSide, 429, "acc-429")
		if got != 15*time.Minute {
			t.Errorf("cap step %d: want 15min, got %v", i, got)
		}
	}
}

func TestCooldownPolicy_429PerAccount(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	// 不同账号独立计数
	if got := p.ComputeCooldown(ErrAccountSide, 429, "acc-a"); got != 10*time.Second {
		t.Errorf("acc-a first: want 10s, got %v", got)
	}
	if got := p.ComputeCooldown(ErrAccountSide, 429, "acc-b"); got != 10*time.Second {
		t.Errorf("acc-b first: want 10s, got %v", got)
	}
	if got := p.ComputeCooldown(ErrAccountSide, 429, "acc-a"); got != 30*time.Second {
		t.Errorf("acc-a second: want 30s, got %v", got)
	}
}

func TestCooldownPolicy_Reset(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	// 累积到中间级别
	p.ComputeCooldown(ErrAccountSide, 429, "acc-reset")
	p.ComputeCooldown(ErrAccountSide, 429, "acc-reset")
	p.ComputeCooldown(ErrAccountSide, 429, "acc-reset")

	p.Reset("acc-reset")

	// Reset 后应该从头开始
	got := p.ComputeCooldown(ErrAccountSide, 429, "acc-reset")
	if got != 10*time.Second {
		t.Errorf("after Reset: want 10s, got %v", got)
	}
}

func TestCooldownPolicy_Cap(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	// 即使是 403 也不超过 cap
	got := p.ComputeCooldown(ErrAccountSide, 403, "acc-cap")
	if got > CooldownCap {
		t.Errorf("403 exceeded cap: %v > %v", got, CooldownCap)
	}
}

func TestCooldownPolicy_Snapshot(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	p.ComputeCooldown(ErrAccountSide, 429, "acc-1")
	p.ComputeCooldown(ErrAccountSide, 429, "acc-1")
	p.ComputeCooldown(ErrAccountSide, 429, "acc-2")

	snap := p.Snapshot()
	if snap["acc-1"] != 2 {
		t.Errorf("acc-1 level: want 2, got %d", snap["acc-1"])
	}
	if snap["acc-2"] != 1 {
		t.Errorf("acc-2 level: want 1, got %d", snap["acc-2"])
	}
}

func TestCooldownPolicy_GC(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	p.ComputeCooldown(ErrAccountSide, 429, "acc-stale")
	// 手动调整 lastFailAt 模拟空闲
	p.mu.Lock()
	if b, ok := p.backoffs["acc-stale"]; ok {
		b.lastFailAt = time.Now().Add(-2 * time.Hour)
	}
	p.mu.Unlock()

	p.gc()

	snap := p.Snapshot()
	if _, exists := snap["acc-stale"]; exists {
		t.Error("stale entry should be GC'd")
	}
}

func TestCooldownPolicy_Concurrent(t *testing.T) {
	p := NewCooldownPolicy()
	defer p.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p.ComputeCooldown(ErrAccountSide, 429, "acc-concurrent")
				p.Reset("acc-concurrent")
			}
		}(i)
	}
	wg.Wait()
	// 只要不 panic / 不 data race 就算过
}

func TestCooldownPolicy_CloseIdempotent(t *testing.T) {
	p := NewCooldownPolicy()
	p.Close()
	p.Close() // 不应 panic
	p.Close()
}
