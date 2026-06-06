package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/config"
)

func TestUpstreamPoolReusesIdleConnectionForSameKey(t *testing.T) {
	pool := NewUpstreamPool(config.WSServerConfig{
		PoolMaxTotalConns:      2,
		PoolMaxIdlePerKey:      1,
		PoolIdleTimeoutSeconds: 60,
		PoolMaxConnLifetime:    60,
	})
	key := PoolKey{Provider: "openai", AccountID: "acc", Endpoint: "https://api.openai.com", SessionID: "sess-1"}
	dials := 0
	dial := func() (*UpstreamConn, error) {
		dials++
		conn := &UpstreamConn{provider: key.Provider, keyID: key.AccountID, endpoint: key.Endpoint, sessionID: key.SessionID, createdAt: time.Now()}
		conn.lastUsed.Store(time.Now().Unix())
		return conn, nil
	}

	first, err := pool.Get(key, dial)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	pool.Put(first)
	second, err := pool.Get(key, dial)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first != second {
		t.Fatalf("pool did not reuse idle connection")
	}
	if dials != 1 {
		t.Fatalf("dials = %d, want 1", dials)
	}
	pool.Discard(second)
}

func TestUpstreamPoolExpiresIdleConnection(t *testing.T) {
	pool := NewUpstreamPool(config.WSServerConfig{
		PoolMaxTotalConns:      2,
		PoolMaxIdlePerKey:      1,
		PoolIdleTimeoutSeconds: 1,
		PoolMaxConnLifetime:    60,
	})
	key := PoolKey{Provider: "openai", AccountID: "acc", Endpoint: "https://api.openai.com", SessionID: "sess-1"}
	dials := 0
	dial := func() (*UpstreamConn, error) {
		dials++
		conn := &UpstreamConn{provider: key.Provider, keyID: key.AccountID, endpoint: key.Endpoint, sessionID: key.SessionID, createdAt: time.Now().Add(-2 * time.Second)}
		conn.lastUsed.Store(time.Now().Add(-2 * time.Second).Unix())
		return conn, nil
	}

	first, err := pool.Get(key, dial)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	pool.Put(first)
	second, err := pool.Get(key, dial)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first == second {
		t.Fatalf("expired idle connection was reused")
	}
	if dials != 2 {
		t.Fatalf("dials = %d, want 2", dials)
	}
	pool.Discard(second)
}

func TestUpstreamPoolEvictsIdleConnectionWhenTotalLimitReached(t *testing.T) {
	pool := NewUpstreamPool(config.WSServerConfig{
		PoolMaxTotalConns:      1,
		PoolMaxIdlePerKey:      1,
		PoolIdleTimeoutSeconds: 60,
		PoolMaxConnLifetime:    60,
	})
	keyA := PoolKey{Provider: "openai", AccountID: "acc-a", Endpoint: "https://api.openai.com", SessionID: "sess-a"}
	keyB := PoolKey{Provider: "openai", AccountID: "acc-b", Endpoint: "https://api.openai.com", SessionID: "sess-b"}
	dials := 0
	dialFor := func(key PoolKey) func() (*UpstreamConn, error) {
		return func() (*UpstreamConn, error) {
			dials++
			conn := &UpstreamConn{provider: key.Provider, keyID: key.AccountID, endpoint: key.Endpoint, sessionID: key.SessionID, createdAt: time.Now()}
			conn.lastUsed.Store(time.Now().Unix())
			return conn, nil
		}
	}

	first, err := pool.Get(keyA, dialFor(keyA))
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	pool.Put(first)
	second, err := pool.Get(keyB, dialFor(keyB))
	if err != nil {
		t.Fatalf("second Get should evict idle connection instead of failing: %v", err)
	}
	if second == first {
		t.Fatalf("new key reused connection for a different key")
	}
	if dials != 2 {
		t.Fatalf("dials = %d, want 2", dials)
	}
	pool.Discard(second)
}

func TestUpstreamPoolRemoveSessionClosesIdleConnections(t *testing.T) {
	pool := NewUpstreamPool(config.WSServerConfig{
		PoolMaxTotalConns:      2,
		PoolMaxIdlePerKey:      1,
		PoolIdleTimeoutSeconds: 60,
		PoolMaxConnLifetime:    60,
	})
	key := PoolKey{Provider: "openai", AccountID: "acc", Endpoint: "https://api.openai.com", SessionID: "sess-1"}
	conn := &UpstreamConn{provider: key.Provider, keyID: key.AccountID, endpoint: key.Endpoint, sessionID: key.SessionID, createdAt: time.Now()}
	conn.lastUsed.Store(time.Now().Unix())

	got, err := pool.Get(key, func() (*UpstreamConn, error) { return conn, nil })
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	pool.Put(got)
	pool.RemoveSession("sess-1")
	if !conn.IsClosed() {
		t.Fatalf("session idle connection was not closed")
	}
	if total := pool.InFlight(); total != 0 {
		t.Fatalf("pool total = %d, want 0", total)
	}
}
