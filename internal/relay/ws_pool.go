package relay

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/AutoCONFIG/uapi/internal/config"
	ws "github.com/fasthttp/websocket"
)

// ── Upstream Pool ───────────────────────────────────────────────────────────────
//
// Each upstream WS connection is EXCLUSIVE while a turn is active. Cleanly
// completed turns may return the connection to the idle pool for the same key;
// errors, client aborts, and terminal failures discard it.

type PoolKey struct {
	Provider  string
	AccountID string
	Endpoint  string
	SessionID string
}

// UpstreamConn wraps a WebSocket connection to an upstream provider.
type UpstreamConn struct {
	conn      *ws.Conn
	provider  string
	keyID     string // account ID
	endpoint  string
	sessionID string
	createdAt time.Time
	lastUsed  atomic.Int64
	writeMu   sync.Mutex // serialise writes to upstream conn
	closed    atomic.Bool
}

// Close closes the underlying WebSocket connection.
func (uc *UpstreamConn) Close() error {
	if uc.closed.Swap(true) {
		return nil
	}
	return closeUpstreamWSConn(uc.conn)
}

// IsClosed returns whether the connection has been closed.
func (uc *UpstreamConn) IsClosed() bool {
	return uc.closed.Load()
}

// UpstreamPool provides keyed idle pooling for upstream WS connections.
type UpstreamPool struct {
	mu    sync.Mutex
	idle  map[PoolKey][]*UpstreamConn
	total int
	cfg   config.WSServerConfig
}

func NewUpstreamPool(cfg config.WSServerConfig) *UpstreamPool {
	return &UpstreamPool{cfg: cfg, idle: make(map[PoolKey][]*UpstreamConn)}
}

// Get returns an idle connection for key or creates a new one using dialFn.
func (p *UpstreamPool) Get(key PoolKey, dialFn func() (*UpstreamConn, error)) (*UpstreamConn, error) {
	p.mu.Lock()
	toClose := p.pruneLocked(time.Now())
	if conns := p.idle[key]; len(conns) > 0 {
		conn := conns[len(conns)-1]
		if len(conns) == 1 {
			delete(p.idle, key)
		} else {
			p.idle[key] = conns[:len(conns)-1]
		}
		p.mu.Unlock()
		closeUpstreamWSConns(toClose)
		return conn, nil
	}

	maxTotal := p.cfg.PoolMaxTotalConns
	if maxTotal > 0 && p.total >= maxTotal {
		if conn := p.evictOldestIdleLocked(); conn != nil {
			toClose = append(toClose, conn)
			if p.total < maxTotal {
				p.total++
				p.mu.Unlock()
				closeUpstreamWSConns(toClose)
				conn, err := dialFn()
				if err != nil {
					p.mu.Lock()
					p.total--
					p.mu.Unlock()
					return nil, err
				}
				return conn, nil
			}
		}
		if p.total >= maxTotal {
			p.mu.Unlock()
			closeUpstreamWSConns(toClose)
			return nil, errPoolExhausted
		}
	}
	p.total++
	p.mu.Unlock()
	closeUpstreamWSConns(toClose)

	conn, err := dialFn()
	if err != nil {
		p.mu.Lock()
		p.total--
		p.mu.Unlock()
		return nil, err
	}
	return conn, nil
}

// Put returns a cleanly completed connection to the idle pool.
func (p *UpstreamPool) Put(conn *UpstreamConn) {
	if conn == nil || conn.IsClosed() {
		return
	}
	now := time.Now()
	if p.connExpired(conn, now) {
		p.Discard(conn)
		return
	}
	conn.lastUsed.Store(now.Unix())
	key := PoolKey{Provider: conn.provider, AccountID: conn.keyID, Endpoint: conn.endpoint, SessionID: conn.sessionID}

	p.mu.Lock()
	maxIdle := p.cfg.PoolMaxIdlePerKey
	if maxIdle <= 0 {
		maxIdle = 1
	}
	if len(p.idle[key]) >= maxIdle {
		p.mu.Unlock()
		p.Discard(conn)
		return
	}
	p.idle[key] = append(p.idle[key], conn)
	p.mu.Unlock()
}

// RemoveSession closes idle connections associated with a downstream WS session.
func (p *UpstreamPool) RemoveSession(sessionID string) {
	if sessionID == "" {
		return
	}
	var toClose []*ws.Conn
	p.mu.Lock()
	for key, conns := range p.idle {
		if key.SessionID != sessionID {
			continue
		}
		for _, conn := range conns {
			if conn == nil || !conn.closed.CompareAndSwap(false, true) {
				continue
			}
			toClose = append(toClose, conn.conn)
			if p.total > 0 {
				p.total--
			}
		}
		delete(p.idle, key)
	}
	p.mu.Unlock()
	closeUpstreamWSConns(toClose)
}

// Discard removes a connection and decrements total count.
// Safe to call multiple times — only decrements once.
func (p *UpstreamPool) Discard(conn *UpstreamConn) {
	if conn == nil {
		return
	}
	if !conn.closed.CompareAndSwap(false, true) {
		return // already discarded
	}
	_ = closeUpstreamWSConn(conn.conn)
	p.mu.Lock()
	if p.total > 0 {
		p.total--
	}
	p.mu.Unlock()
}

func closeUpstreamWSConn(conn *ws.Conn) error {
	if conn == nil {
		return nil
	}
	_ = conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
	return conn.Close()
}

func closeUpstreamWSConns(conns []*ws.Conn) {
	for _, conn := range conns {
		_ = closeUpstreamWSConn(conn)
	}
}

func (p *UpstreamPool) pruneLocked(now time.Time) []*ws.Conn {
	var toClose []*ws.Conn
	for key, conns := range p.idle {
		kept := conns[:0]
		for _, conn := range conns {
			if conn == nil || conn.IsClosed() || p.connExpired(conn, now) {
				if conn != nil && conn.closed.CompareAndSwap(false, true) {
					toClose = append(toClose, conn.conn)
					if p.total > 0 {
						p.total--
					}
				}
				continue
			}
			kept = append(kept, conn)
		}
		if len(kept) == 0 {
			delete(p.idle, key)
		} else {
			p.idle[key] = kept
		}
	}
	return toClose
}

func (p *UpstreamPool) evictOldestIdleLocked() *ws.Conn {
	var oldestKey PoolKey
	var oldestIndex int
	var oldestTime time.Time
	found := false
	for key, conns := range p.idle {
		for index, conn := range conns {
			if conn == nil {
				oldestKey = key
				oldestIndex = index
				found = true
				break
			}
			last := time.Unix(conn.lastUsed.Load(), 0)
			if !found || last.Before(oldestTime) {
				oldestKey = key
				oldestIndex = index
				oldestTime = last
				found = true
			}
		}
		if found && p.idle[oldestKey][oldestIndex] == nil {
			break
		}
	}
	if !found {
		return nil
	}
	conn := p.idle[oldestKey][oldestIndex]
	conns := p.idle[oldestKey]
	conns = append(conns[:oldestIndex], conns[oldestIndex+1:]...)
	if len(conns) == 0 {
		delete(p.idle, oldestKey)
	} else {
		p.idle[oldestKey] = conns
	}
	if conn == nil || !conn.closed.CompareAndSwap(false, true) {
		return nil
	}
	if p.total > 0 {
		p.total--
	}
	return conn.conn
}

func (p *UpstreamPool) connExpired(conn *UpstreamConn, now time.Time) bool {
	if conn == nil {
		return true
	}
	if maxAge := time.Duration(p.cfg.PoolMaxConnLifetime) * time.Second; maxAge > 0 && now.Sub(conn.createdAt) >= maxAge {
		return true
	}
	if idle := time.Duration(p.cfg.PoolIdleTimeoutSeconds) * time.Second; idle > 0 {
		last := conn.lastUsed.Load()
		if last > 0 && now.Sub(time.Unix(last, 0)) >= idle {
			return true
		}
	}
	return false
}

// Close closes all idle connections.
func (p *UpstreamPool) Close() {
	var toClose []*ws.Conn
	p.mu.Lock()
	for key, conns := range p.idle {
		for _, conn := range conns {
			if conn == nil || !conn.closed.CompareAndSwap(false, true) {
				continue
			}
			toClose = append(toClose, conn.conn)
			if p.total > 0 {
				p.total--
			}
		}
		delete(p.idle, key)
	}
	p.mu.Unlock()
	closeUpstreamWSConns(toClose)
}

// InFlight returns the current number of live upstream connections.
func (p *UpstreamPool) InFlight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

var errPoolExhausted = &poolError{msg: "upstream connection limit reached"}

type poolError struct {
	msg string
}

func (e *poolError) Error() string { return e.msg }
