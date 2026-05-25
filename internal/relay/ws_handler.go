package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	ws "github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if cfg.MaxMessageSizeMB == 0 {
		cfg.MaxMessageSizeMB = 256
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
	if h.relayer != nil && h.relayer.requireInternal {
		ctx.Error(`{"error":"gateway signature required"}`, fasthttp.StatusUnauthorized)
		return
	}

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
		if !checkIPWhitelist(ctx, token.IPWhitelist, h.relayer.trustedProxies) {
			ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
			return
		}
	}
	policy, hasPolicy, err := h.loadWSPolicy(token)
	if err != nil {
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusForbidden)
		return
	}
	models := token.Models
	var sessionPolicy *wsSessionPolicy
	if hasPolicy {
		models = policy.AllowedModels
		sessionPolicy = &wsSessionPolicy{
			id:             policy.ID.String(),
			allowedModels:  policy.AllowedModels,
			maxConcurrency: policy.MaxConcurrency,
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
	err = h.upgrader.Upgrade(ctx, func(conn *ws.Conn) {
		// Create session
		sess, sErr := h.sessions.Create(tokenID, tokenKey, token.UserID, models, token.Permissions, sessionPolicy, conn)
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

func (h *WSHandler) loadWSPolicy(token db.Token) (db.AccessPolicy, bool, error) {
	policyID, err := h.planPolicyID(token.ID)
	if err != nil {
		return db.AccessPolicy{}, false, err
	}
	if policyID == nil || *policyID == uuid.Nil {
		return db.AccessPolicy{}, false, nil
	}
	var policy db.AccessPolicy
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *policyID).First(&policy).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return db.AccessPolicy{}, true, fmt.Errorf("access policy disabled or not found")
		}
		return db.AccessPolicy{}, true, err
	}
	return policy, true, nil
}

func (h *WSHandler) planPolicyID(tokenID uuid.UUID) (*uuid.UUID, error) {
	var row struct {
		PolicyID *uuid.UUID
	}
	if err := h.db.Table("token_plans").
		Select("plans.policy_id").
		Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
		Where("token_plans.token_id = ? AND token_plans.starts_at <= ? AND token_plans.expires_at > ?", tokenID, time.Now(), time.Now()).
		Order("token_plans.created_at DESC").
		Limit(1).
		Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.PolicyID == nil || *row.PolicyID == uuid.Nil {
		return nil, nil
	}
	return row.PolicyID, nil
}

// eventLoop reads messages from the client and dispatches them.
func (h *WSHandler) eventLoop(sess *Session) {
	defer func() {
		sess.Close()
		sess.clientConn.Close()
		h.sessions.Remove(sess)
	}()

	sess.clientConn.SetReadLimit(int64(h.cfg.MaxMessageSizeMB) * 1024 * 1024)
	sess.clientConn.SetReadDeadline(time.Time{}) // no deadline for reading

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
			WriteWSErrorSession(sess, 400, "invalid_event", "invalid JSON event")
			continue
		}

		switch envelope.Type {
		case WSEventResponseCreate:
			if !sess.TryAcquireTurn() {
				WriteWSErrorSession(sess, 429, "turn_in_progress",
					"a response turn is already in progress")
				continue
			}
			h.handleResponseCreate(sess, msg)
		default:
			WriteWSErrorSession(sess, 400, "unsupported_event",
				"unsupported event type: "+envelope.Type)
		}
	}
}

// handleResponseCreate processes a response.create event from the client.
// The caller (eventLoop) is responsible for acquiring the turn semaphore via
// sess.TryAcquireTurn(). Synchronous failures release it here; async proxy paths
// release it when the upstream turn actually completes.
func (h *WSHandler) handleResponseCreate(sess *Session, msg []byte) {
	releaseTurn := true
	defer func() {
		if releaseTurn {
			sess.ReleaseTurn()
		}
	}()

	model := ParseModelFromCreateEvent(msg)
	if model == "" {
		WriteWSErrorSession(sess, 400, "model_required", "model is required")
		return
	}

	// Check model permissions
	if sess.models != "" && !modelInList(model, sess.models) {
		WriteWSErrorSession(sess, 403, "model_forbidden", "model not allowed for token")
		return
	}
	if sess.permissions != "" && !permissionInList("responses", sess.permissions) {
		WriteWSErrorSession(sess, 403, "permission_forbidden", "responses permission not allowed")
		return
	}
	if sess.policy != nil {
		if sess.policy.allowedModels != "" && !modelInList(model, sess.policy.allowedModels) {
			WriteWSErrorSession(sess, 403, "model_forbidden", "model not allowed for policy")
			return
		}
		if sess.policy.maxConcurrency > 0 {
			limitKey := "policy:" + sess.policy.id
			if !h.concLimiter.AcquireWithLimit(limitKey, sess.policy.maxConcurrency) {
				WriteWSErrorSession(sess, 429, "policy_concurrency", "policy concurrent request limit exceeded")
				return
			}
			sess.SetTurnRelease(func() { h.concLimiter.Release(limitKey) })
		}
		if err := h.checkWSPolicyWindows(sess.policy.id, sess.tokenID); err != nil {
			WriteWSErrorSession(sess, 429, "policy_limit", err.Error())
			return
		}
	}

	// Resolve channel and account (reuse relayer logic)
	ch, account, adaptor, creds, err := h.relayer.resolveChannelAndAccount(sess.tokenID, model)
	if err != nil {
		WriteWSErrorSession(sess, 404, "no_channel", err.Error())
		return
	}

	// Per-turn billing (after successful channel resolution)
	estTokens := EstimateTokensFromCreateEvent(msg)
	var tokenPlanID uuid.UUID
	if h.billing != nil {
		planID, err := h.billing.PreConsume(sess.tokenID, model, estTokens)
		if err != nil {
			logger.Component("relay.ws").Warn("billing pre-consume error", logger.F("token_id", sess.tokenID), logger.F("model", model), logger.Err(err))
			WriteWSErrorSession(sess, 429, "billing_error", "pre-consume failed")
			return
		}
		tokenPlanID = planID
	}

	start := time.Now()

	// Decide: native WS upstream or HTTP bridge
	// Native WS only for OpenAI Responses format channels
	if ch.Type == "openai" && ch.APIFormat == "responses" {
		if h.tryNativeUpstream(sess, msg, ch, account, creds, model, estTokens, tokenPlanID, start) {
			releaseTurn = false
			return
		}
	}

	// Fallback: HTTP-SSE bridge
	if h.httpBridgeFallback(sess, msg, ch, account, adaptor, creds, model, estTokens, tokenPlanID, start) {
		releaseTurn = false
	}
}

func (h *WSHandler) checkWSPolicyWindows(policyID, tokenID string) error {
	pid, err := uuid.Parse(policyID)
	if err != nil {
		return err
	}
	tid, err := uuid.Parse(tokenID)
	if err != nil {
		return err
	}
	var policy db.AccessPolicy
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", pid).First(&policy).Error; err != nil {
		return err
	}
	windows := []struct {
		typeName string
		limit    int
		start    time.Time
	}{
		{"hour", policy.HourlyLimit, wsCurrentHour()},
		{"week", policy.WeeklyLimit, wsCurrentWeek()},
		{"month", policy.MonthlyLimit, wsCurrentMonth()},
	}
	return h.db.Transaction(func(tx *gorm.DB) error {
		for _, w := range windows {
			if w.limit <= 0 {
				continue
			}
			var usage db.PolicyUsageWindow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("policy_id = ? AND token_id = ? AND window_type = ? AND window_start = ?", pid, tid, w.typeName, w.start).
				First(&usage).Error
			if err != nil {
				if err != gorm.ErrRecordNotFound {
					return err
				}
				newUsage := db.PolicyUsageWindow{
					ID:          uuid.New(),
					PolicyID:    pid,
					TokenID:     tid,
					WindowType:  w.typeName,
					WindowStart: w.start,
					UsedCount:   0,
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newUsage).Error; err != nil {
					return err
				}
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("policy_id = ? AND token_id = ? AND window_type = ? AND window_start = ?", pid, tid, w.typeName, w.start).
					First(&usage).Error; err != nil {
					return err
				}
			}
			if usage.UsedCount >= w.limit {
				return fmt.Errorf("%s request limit exceeded", w.typeName)
			}
			if err := tx.Model(&usage).Update("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func wsCurrentHour() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)
}

func wsCurrentWeek() time.Time {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
}

func wsCurrentMonth() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ── Per-turn billing helpers ───────────────────────────────────────────────────

func (h *WSHandler) settleBilling(tokenID string, tokenPlanID uuid.UUID, estTokens, promptTokens, completionTokens int, model string) {
	if h.billing == nil {
		return
	}
	go func() {
		if err := h.billing.DBTransactionRefundAndSettle(tokenID, tokenPlanID, estTokens, promptTokens, completionTokens, 0, 0, model); err != nil {
			logger.Component("relay.ws").Warn("billing settle error", logger.F("token_id", tokenID), logger.F("model", model), logger.Err(err))
		}
	}()
}

func (h *WSHandler) refundBilling(tokenID string, tokenPlanID uuid.UUID, estTokens int) {
	if h.billing == nil || estTokens == 0 {
		return
	}
	go func() {
		if err := h.billing.DBTransactionRefund(tokenID, tokenPlanID, estTokens); err != nil {
			logger.Component("relay.ws").Warn("billing refund failed",
				logger.F("token_id", tokenID),
				logger.F("error", err.Error()),
			)
		}
	}()
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
