package relay

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	ws "github.com/fasthttp/websocket"
)

// ── Client Session ─────────────────────────────────────────────────────────────

// Session represents a single client WebSocket connection.
type Session struct {
	id            string
	clientConn    *ws.Conn
	writeMu       sync.Mutex // serialise writes to client conn
	tokenID       string
	tokenKey      string
	userID        string
	models        string // cached token.Models for permission checks
	permissions   string // cached token.Permissions
	closed        atomic.Bool
	writeDeadline time.Duration // timeout for writes; 0 = no deadline
	inFlight      atomic.Int64  // number of in-flight response turns
}

func newSession(id, tokenID, tokenKey, userID, models, permissions string, conn *ws.Conn, writeDeadline time.Duration) *Session {
	return &Session{
		id:            id,
		clientConn:    conn,
		tokenID:       tokenID,
		tokenKey:      tokenKey,
		userID:        userID,
		models:        models,
		permissions:   permissions,
		writeDeadline: writeDeadline,
	}
}

// WriteMessage sends a message to the client connection (thread-safe).
// Sets a write deadline to prevent indefinite blocking on slow clients.
func (s *Session) WriteMessage(msgType int, data []byte) error {
	if s.closed.Load() {
		return ws.ErrCloseSent
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeDeadline > 0 {
		s.clientConn.SetWriteDeadline(time.Now().Add(s.writeDeadline))
		defer s.clientConn.SetWriteDeadline(time.Time{})
	}
	return s.clientConn.WriteMessage(msgType, data)
}

// Close sends a close frame and marks the session as closed.
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return nil // already closed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.clientConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.clientConn.WriteMessage(ws.CloseMessage,
		ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
}

// IsClosed returns whether the session has been closed.
func (s *Session) IsClosed() bool {
	return s.closed.Load()
}

// TryAcquireTurn atomically increments the in-flight turn counter.
// Returns false if there is already a turn in flight (only one turn at a time per session).
func (s *Session) TryAcquireTurn() bool {
	return s.inFlight.CompareAndSwap(0, 1)
}

// ReleaseTurn atomically decrements the in-flight turn counter.
func (s *Session) ReleaseTurn() {
	s.inFlight.Store(0)
}

// ── Session Manager ────────────────────────────────────────────────────────────

// SessionManager tracks all active WebSocket sessions.
type SessionManager struct {
	mu            sync.RWMutex
	sessions      map[string]*Session // session id → session
	byToken       map[string]int      // token id → count
	maxConns      int                 // 0 = unlimited
	writeDeadline time.Duration       // write deadline for client connections
}

func NewSessionManager(maxConns int, writeDeadline time.Duration) *SessionManager {
	return &SessionManager{
		sessions:      make(map[string]*Session),
		byToken:       make(map[string]int),
		maxConns:      maxConns,
		writeDeadline: writeDeadline,
	}
}

var errWSConnLimit = &wsCloseError{code: ws.CloseTryAgainLater, msg: "connection limit exceeded"}

type wsCloseError struct {
	code int
	msg  string
}

func (e *wsCloseError) Error() string { return e.msg }

// Create creates and registers a new session. Returns error if the connection limit is exceeded.
func (sm *SessionManager) Create(tokenID, tokenKey, userID, models, permissions string, conn *ws.Conn) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.maxConns > 0 && len(sm.sessions) >= sm.maxConns {
		return nil, errWSConnLimit
	}

	id := provider.RandomHex(16)
	sess := newSession(id, tokenID, tokenKey, userID, models, permissions, conn, sm.writeDeadline)
	sm.sessions[id] = sess
	sm.byToken[tokenID]++
	return sess, nil
}

// Remove unregisters a session.
func (sm *SessionManager) Remove(sess *Session) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.sessions[sess.id]; !ok {
		return
	}
	delete(sm.sessions, sess.id)
	if cnt, ok := sm.byToken[sess.tokenID]; ok {
		cnt--
		if cnt <= 0 {
			delete(sm.byToken, sess.tokenID)
		} else {
			sm.byToken[sess.tokenID] = cnt
		}
	}
}

// CloseAll gracefully closes all sessions.
func (sm *SessionManager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for id, sess := range sm.sessions {
		sess.Close()
		delete(sm.sessions, id)
	}
	sm.byToken = make(map[string]int)
}

// Count returns the number of active sessions.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// TokenCount returns the number of sessions for a given token.
func (sm *SessionManager) TokenCount(tokenID string) int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.byToken[tokenID]
}

