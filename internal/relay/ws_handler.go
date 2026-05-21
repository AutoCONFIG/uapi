package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	ws "github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// WSHandler handles WebSocket connections for the /v1/responses endpoint.
type WSHandler struct {
	db          *gorm.DB
	billing     *BillingService
	concLimiter *ConcurrencyLimiter
	sessions    *SessionManager
	relayer     *Relayer
	upstream    *UpstreamPool
	upgrader    ws.FastHTTPUpgrader
	cfg         config.WSServerConfig
}

func NewWSHandler(database *gorm.DB, billing *BillingService, relayer *Relayer, cfg config.WSServerConfig) *WSHandler {
	if cfg.PoolIdleTimeoutSeconds == 0 {
		cfg.PoolIdleTimeoutSeconds = 300
	}
	if cfg.PoolMaxConnLifetime == 0 {
		cfg.PoolMaxConnLifetime = 1800
	}
	if cfg.PoolMaxTotalConns == 0 {
		cfg.PoolMaxTotalConns = 100
	}
	if cfg.PoolMaxIdlePerKey == 0 {
		cfg.PoolMaxIdlePerKey = 5
	}
	if cfg.StreamIdleTimeoutSeconds == 0 {
		cfg.StreamIdleTimeoutSeconds = 120
	}

	allowedOrigins := cfg.AllowedOrigins
	checkOrigin := func(ctx *fasthttp.RequestCtx) bool {
		if len(allowedOrigins) == 0 {
			return true
		}
		origin := string(ctx.Request.Header.Peek("Origin"))
		for _, o := range allowedOrigins {
			if o == "*" || o == origin {
				return true
			}
		}
		return false
	}

	h := &WSHandler{
		db:          database,
		billing:     billing,
		concLimiter: relayer.concLimiter,
		sessions:    NewSessionManager(cfg.MaxConnections, 10*time.Second),
		relayer:     relayer,
		cfg:         cfg,
		upgrader: ws.FastHTTPUpgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     checkOrigin,
		},
	}
	h.upstream = NewUpstreamPool(cfg)
	return h
}

// SetRelayer wires the relayer after construction (breaks circular dependency).
func (h *WSHandler) SetRelayer(r *Relayer) {
	h.relayer = r
}

// Close shuts down the WS handler gracefully.
func (h *WSHandler) Close() {
	h.sessions.CloseAll()
	h.upstream.Close()
}

// HandleUpgrade handles the WebSocket upgrade for /v1/responses.
func (h *WSHandler) HandleUpgrade(ctx *fasthttp.RequestCtx) {
	// 1. Extract Bearer token from header or query param
	tokenKey := extractBearerToken(ctx)
	if tokenKey == "" {
		tokenKey = string(ctx.QueryArgs().Peek("token"))
	}
	if tokenKey == "" {
		ctx.Error(`{"error":"missing authorization"}`, fasthttp.StatusUnauthorized)
		return
	}

	// 2. Validate token
	var token db.Token
	if err := h.db.Where("key = ? AND enabled = true AND deleted_at IS NULL", tokenKey).First(&token).Error; err != nil {
		ctx.Error(`{"error":"invalid token"}`, fasthttp.StatusUnauthorized)
		return
	}
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		ctx.Error(`{"error":"token expired"}`, fasthttp.StatusUnauthorized)
		return
	}

	// 3. IP whitelist check
	if token.IPWhitelist != "" {
		if !checkIPWhitelist(ctx, token.IPWhitelist) {
			ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
			return
		}
	}

	// 4. Concurrency check for upgrade
	tokenID := token.ID.String()
	if !h.concLimiter.Acquire(tokenID) {
		ctx.Error(`{"error":"concurrent request limit exceeded"}`, 429)
		return
	}

	// 5. Billing limit check
	if h.billing != nil {
		if err := h.billing.CheckLimit(tokenID); err != nil {
			h.concLimiter.Release(tokenID)
			ctx.Error(`{"error":"rate limit exceeded"}`, 429)
			return
		}
		if token.UserID != "" {
			if err := h.billing.CheckUserBalance(token.UserID, tokenID); err != nil {
				h.concLimiter.Release(tokenID)
				ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, 402)
				return
			}
		}
	}

	// 6. Upgrade to WebSocket
	err := h.upgrader.Upgrade(ctx, func(conn *ws.Conn) {
		// Create session
		sess, sErr := h.sessions.Create(tokenID, tokenKey, token.UserID, token.Models, token.Permissions, conn)
		if sErr != nil {
			logger.Component("relay.ws").Warn("session create failed", logger.F("token_id", tokenID), logger.Err(sErr))
			h.concLimiter.Release(tokenID)
			conn.WriteMessage(ws.CloseMessage,
				ws.FormatCloseMessage(ws.CloseTryAgainLater, sErr.Error()))
			conn.Close()
			return
		}

		// Release the initial concurrency slot — WS uses per-turn concurrency
		h.concLimiter.Release(tokenID)

		h.eventLoop(sess)
	})
	if err != nil {
		logger.Component("relay.ws").Warn("upgrade failed", logger.F("token_id", tokenID), logger.Err(err))
		h.concLimiter.Release(tokenID)
	}
}

// eventLoop reads messages from the client and dispatches them.
func (h *WSHandler) eventLoop(sess *Session) {
	defer func() {
		sess.Close()
		sess.clientConn.Close()
		h.sessions.Remove(sess)
	}()

	sess.clientConn.SetReadLimit(10 * 1024 * 1024) // 10MB max message
	sess.clientConn.SetReadDeadline(time.Time{})   // no deadline for reading

	for {
		msgType, msg, err := sess.clientConn.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, ws.CloseGoingAway, ws.CloseNormalClosure) {
				logger.Component("relay.ws").Warn("client read error", logger.F("session", sess.id), logger.Err(err))
			}
			return
		}
		if msgType != ws.TextMessage && msgType != ws.BinaryMessage {
			continue
		}

		// Parse event type only
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			WriteWSError(sess.clientConn, 400, "invalid_event", "invalid JSON event")
			continue
		}

		switch envelope.Type {
		case WSEventResponseCreate:
			if !sess.TryAcquireTurn() {
				WriteWSError(sess.clientConn, 429, "turn_in_progress",
					"a response turn is already in progress")
				continue
			}
			h.handleResponseCreate(sess, msg)
		default:
			WriteWSError(sess.clientConn, 400, "unsupported_event",
				"unsupported event type: "+envelope.Type)
		}
	}
}

// handleResponseCreate processes a response.create event from the client.
// The caller (eventLoop) is responsible for acquiring the turn semaphore via
// sess.TryAcquireTurn(). This function MUST call sess.ReleaseTurn() before returning.
func (h *WSHandler) handleResponseCreate(sess *Session, msg []byte) {
	defer sess.ReleaseTurn()

	model := ParseModelFromCreateEvent(msg)
	if model == "" {
		WriteWSError(sess.clientConn, 400, "model_required", "model is required")
		return
	}

	// Check model permissions
	if sess.models != "" && !modelInList(model, sess.models) {
		WriteWSError(sess.clientConn, 403, "model_forbidden", "model not allowed for token")
		return
	}
	if sess.permissions != "" && !permissionInList("responses", sess.permissions) {
		WriteWSError(sess.clientConn, 403, "permission_forbidden", "responses permission not allowed")
		return
	}

	// Resolve channel and account (reuse relayer logic)
	ch, account, adaptor, creds, err := h.relayer.resolveChannelAndAccount(sess.tokenID, model)
	if err != nil {
		WriteWSError(sess.clientConn, 404, "no_channel", err.Error())
		return
	}

	// Per-turn billing (after successful channel resolution)
	estTokens := 1000
	if h.billing != nil {
		if err := h.billing.PreConsume(sess.tokenID, model, estTokens); err != nil {
			logger.Component("relay.ws").Warn("billing pre-consume error", logger.F("token_id", sess.tokenID), logger.F("model", model), logger.Err(err))
		}
	}

	start := time.Now()

	// Decide: native WS upstream or HTTP bridge
	// Native WS only for OpenAI Responses format channels
	if ch.Type == "openai" && ch.APIFormat == "responses" {
		if h.tryNativeUpstream(sess, msg, ch, account, creds, model, estTokens, start) {
			return
		}
	}

	// Fallback: HTTP-SSE bridge
	h.httpBridgeFallback(sess, msg, ch, account, adaptor, creds, model, estTokens, start)
}

// ── Per-turn billing helpers ───────────────────────────────────────────────────

func (h *WSHandler) settleBilling(tokenID string, estTokens, promptTokens, completionTokens int, model string) {
	if h.billing == nil {
		return
	}
	go func() {
		if err := h.billing.RefundAndSettle(tokenID, estTokens, promptTokens, completionTokens, model); err != nil {
			logger.Component("relay.ws").Warn("billing settle error", logger.F("token_id", tokenID), logger.F("model", model), logger.Err(err))
		}
	}()
}

func (h *WSHandler) refundBilling(tokenID string, estTokens int) {
	if h.billing == nil || estTokens == 0 {
		return
	}
	go h.billing.Refund(tokenID, estTokens)
}

func (h *WSHandler) writeWSLog(tokenID, channelID, accountID interface{}, model string, pt, ct int, start time.Time, statusCode int) {
	h.relayer.writeLog(tokenID, channelID, accountID, model, true, pt, ct, start, statusCode)
}

// ── Upstream WS dialer ─────────────────────────────────────────────────────────

// upstreamDialer is the shared dialer for upstream WS connections.
var upstreamDialer = ws.Dialer{
	HandshakeTimeout: 10 * time.Second,
}

// dialUpstreamWS dials an upstream WebSocket connection.
// Only Authorization header is needed — the Responses WS API uses Bearer auth.
func dialUpstreamWS(endpoint, path string, headers http.Header) (*ws.Conn, *http.Response, error) {
	scheme := "wss"
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if strings.HasPrefix(endpoint, "http://") {
		scheme = "ws"
	}
	u := scheme + "://" + host + path
	return upstreamDialer.Dial(u, headers)
}

// ── Turn state ─────────────────────────────────────────────────────────────────

// turnState tracks per-turn state for a response.create cycle.
// Each turn is independent — no concurrent turns share this state.
type turnState struct {
	mu               sync.Mutex
	promptTokens     int
	completionTokens int
	done             bool
}

func newTurnState() *turnState {
	return &turnState{}
}

func (ts *turnState) setUsage(pt, ct int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.promptTokens = pt
	ts.completionTokens = ct
}

func (ts *turnState) markDone() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.done = true
}

func (ts *turnState) isDone() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.done
}

func (ts *turnState) usage() (int, int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.promptTokens, ts.completionTokens
}
