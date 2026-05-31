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
// Each upstream WS connection is EXCLUSIVE per turn — we discard connections
// after a turn completes rather than returning them to the pool. This prevents
// event interleaving when multiple turns share a connection.
// The pool is still useful for acquiring fresh connections, but connections
// are not reused across turns.

type PoolKey struct {
	Provider  string
	AccountID string
	Endpoint  string
}

// UpstreamConn wraps a WebSocket connection to an upstream provider.
type UpstreamConn struct {
	conn      *ws.Conn
	provider  string
	keyID     string // account ID
	endpoint  string
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

// UpstreamPool provides connection pooling for upstream WS connections.
// Since connections are discarded after each turn, the pool mainly tracks
// in-flight connections for monitoring and cleanup.
type UpstreamPool struct {
	mu       sync.Mutex
	inFlight int
	cfg      config.WSServerConfig
}

func NewUpstreamPool(cfg config.WSServerConfig) *UpstreamPool {
	return &UpstreamPool{cfg: cfg}
}

// Get creates a new upstream connection using dialFn.
// Since connections are exclusive per turn, we always dial fresh.
func (p *UpstreamPool) Get(key PoolKey, dialFn func() (*UpstreamConn, error)) (*UpstreamConn, error) {
	p.mu.Lock()
	maxTotal := p.cfg.PoolMaxTotalConns
	if maxTotal > 0 && p.inFlight >= maxTotal {
		p.mu.Unlock()
		return nil, errPoolExhausted
	}
	p.inFlight++
	p.mu.Unlock()

	conn, err := dialFn()
	if err != nil {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
		return nil, err
	}
	return conn, nil
}

// Discard removes a connection and decrements in-flight count.
// Safe to call multiple times — only decrements once.
func (p *UpstreamPool) Discard(conn *UpstreamConn) {
	if !conn.closed.CompareAndSwap(false, true) {
		return // already discarded
	}
	_ = closeUpstreamWSConn(conn.conn)
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
}

func closeUpstreamWSConn(conn *ws.Conn) error {
	_ = conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
	return conn.Close()
}

// Close is kept for API symmetry; connections are managed per turn.
func (p *UpstreamPool) Close() {}

// InFlight returns the current number of in-flight connections.
func (p *UpstreamPool) InFlight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inFlight
}

var errPoolExhausted = &poolError{msg: "upstream connection limit reached"}

type poolError struct {
	msg string
}

func (e *poolError) Error() string { return e.msg }
