package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	ws "github.com/fasthttp/websocket"
	"github.com/google/uuid"
)

// tryNativeUpstream attempts to proxy the response.create event via a native
// WebSocket connection to the upstream OpenAI Responses API.
// Returns true if the upstream connection was established and proxying started.
//
// Protocol: The Responses WS API expects flat events:
//
//	{"type":"response.create","model":"...","input":[...]}
//
// The proxy forwards the client's message as-is and relays server events back.
// Authentication uses Bearer token in the Authorization header.
// No subprotocols or OpenAI-Beta headers are required for Responses WS.
func (h *WSHandler) tryNativeUpstream(
	sess *Session,
	msg []byte,
	ch *db.Channel,
	acc *db.Account,
	creds string,
	model string,
	estTokens int,
	tokenPlanID uuid.UUID,
	start time.Time,
) bool {
	endpoint := strings.TrimSuffix(upstreamconfig.AccountEndpoint(ch, acc), "/")
	upstreamPath := "/v1/responses"

	key := PoolKey{
		Provider:  ch.Type,
		AccountID: acc.ID.String(),
		Endpoint:  endpoint,
	}

	// 1. Refresh credentials (handles OAuth token expiry)
	validCreds, err := EnsureValidCredentials(acc, h.db)
	if err != nil {
		logger.Component("relay.ws").Warn("credential refresh failed", logger.F("session", sess.id), logger.Err(err))
		return false
	}

	// 2. Get or dial upstream connection
	dialFn := func() (*UpstreamConn, error) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+validCreds)

		conn, _, err := dialUpstreamWS(endpoint, upstreamPath, headers)
		if err != nil {
			return nil, err
		}
		uc := &UpstreamConn{
			conn:      conn,
			provider:  ch.Type,
			keyID:     acc.ID.String(),
			endpoint:  endpoint,
			createdAt: time.Now(),
		}
		uc.lastUsed.Store(time.Now().Unix())
		return uc, nil
	}

	upstreamConn, err := h.upstream.Get(key, dialFn)
	if err != nil {
		logger.Component("relay.ws").Warn("upstream pool get failed", logger.F("session", sess.id), logger.Err(err))
		return false
	}

	// 3. Forward response.create to upstream as-is
	// The client sends the flat format that OpenAI expects directly.
	upstreamConn.writeMu.Lock()
	err = upstreamConn.conn.WriteMessage(ws.TextMessage, msg)
	upstreamConn.writeMu.Unlock()
	if err != nil {
		h.upstream.Discard(upstreamConn)
		logger.Component("relay.ws").Warn("upstream write failed", logger.F("session", sess.id), logger.Err(err))
		return false
	}

	// 4. Read responses from upstream and forward to client
	// Each upstream connection is EXCLUSIVE per turn — we don't return it to
	// the pool until the turn completes. This prevents event interleaving.
	ts := newTurnState()

	// Set idle read deadline to detect stalled upstream connections.
	idleTimeout := time.Duration(h.cfg.StreamIdleTimeoutSeconds) * time.Second
	if idleTimeout == 0 {
		idleTimeout = 120 * time.Second
	}
	upstreamConn.conn.SetReadDeadline(time.Now().Add(idleTimeout))

	go h.proxyUpstreamToClient(sess, upstreamConn, ch, acc, model, estTokens, tokenPlanID, start, ts, idleTimeout)

	return true
}

// proxyUpstreamToClient reads events from an upstream WS connection and forwards
// them to the client session. Handles terminal events, billing, and connection cleanup.
func (h *WSHandler) proxyUpstreamToClient(
	sess *Session,
	upstreamConn *UpstreamConn,
	ch *db.Channel,
	acc *db.Account,
	model string,
	estTokens int,
	tokenPlanID uuid.UUID,
	start time.Time,
	ts *turnState,
	idleTimeout time.Duration,
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Default().Panic("relay.ws", "panic in upstream proxy", r, logger.F("session", sess.id))
		}

		// After turn completes, discard the connection (don't return to pool).
		// WS connections may have buffered data from the previous turn,
		// so it's safer to close and let a fresh connection be created for the next turn.
		h.upstream.Discard(upstreamConn)
		sess.ReleaseTurn()

		// If turn never completed (e.g., client disconnect), refund billing
		if !ts.isDone() {
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		}
	}()

	for {
		if sess.IsClosed() {
			return
		}

		// Reset idle deadline before each read — if upstream is silent
		// for idleTimeout, we treat it as a stalled connection.
		upstreamConn.conn.SetReadDeadline(time.Now().Add(idleTimeout))

		_, msg, err := upstreamConn.conn.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, ws.CloseGoingAway, ws.CloseNormalClosure, ws.CloseAbnormalClosure) {
				logger.Component("relay.ws").Warn("upstream read error", logger.F("session", sess.id), logger.Err(err))
			}
			// Upstream closed unexpectedly — treat as error, don't settle
			return
		}

		// Parse the event type
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			// Forward as-is if we can't parse
			if writeErr := sess.WriteMessage(ws.TextMessage, msg); writeErr != nil {
				return
			}
			continue
		}

		// Forward ALL events to client as-is — the upstream sends valid
		// Responses WS events that the client (Codex/Gemini-CLI) understands.
		if writeErr := sess.WriteMessage(ws.TextMessage, msg); writeErr != nil {
			return
		}

		if IsFailureTerminalEvent(envelope.Type) {
			ts.markDone()
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
			return
		}

		// Check for successful terminal events to settle billing.
		if IsSuccessfulTerminalEvent(envelope.Type) {
			// Extract usage from terminal event
			pt, ct := ParseResponsesUsage(msg)
			ts.setUsage(pt, ct)
			ts.markDone()

			// Settle billing
			promptTokens, completionTokens := ts.usage()
			h.settleBilling(sess.tokenID, tokenPlanID, estTokens, promptTokens, completionTokens, model)
			if ch.AffinityTTL > 0 {
				h.relayer.affinity.Set(sess.tokenID, model, ch.ID.String(), ch.AffinityTTL)
			}
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, promptTokens, completionTokens, start, 200)
			return
		}
	}
}
