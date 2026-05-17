package admin

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/auth"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Handler is the main admin handler that holds shared state and provides
// authentication, setup, login, and dashboard endpoints.
type Handler struct {
	db            *gorm.DB
	cfg           *config.Config
	cfgPath       string
	RefreshPool   func(channelID string) // refresh pool for a channel after CRUD
	RemovePool    func(channelID string) // remove pool when channel is deleted/disabled
	setupMu       sync.Mutex
	setupDone     bool
	oauthMu       sync.Mutex
	oauthSessions map[string]*oauthSession
}

// NewHandler creates a new admin Handler.
func NewHandler(database *gorm.DB, cfg *config.Config, cfgPath string, refreshPool func(channelID string), removePool func(channelID string)) *Handler {
	return &Handler{
		db: database, cfg: cfg, cfgPath: cfgPath,
		RefreshPool: refreshPool, RemovePool: removePool,
		oauthSessions: make(map[string]*oauthSession),
	}
}

// jsonResponse writes a success JSON response.
func (h *Handler) jsonResponse(ctx *fasthttp.RequestCtx, status int, data interface{}) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(status)
	body, _ := json.Marshal(map[string]interface{}{
		"code":    0,
		"data":    data,
		"message": "ok",
	})
	ctx.SetBody(body)
}

// jsonError writes an error JSON response.
func (h *Handler) jsonError(ctx *fasthttp.RequestCtx, status int, msg string) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(status)
	body, _ := json.Marshal(map[string]interface{}{
		"code":    status,
		"message": msg,
	})
	ctx.SetBody(body)
}

// parsePagination extracts page and limit from query parameters.
func (h *Handler) parsePagination(ctx *fasthttp.RequestCtx) (page, limit int) {
	pageStr := string(ctx.QueryArgs().Peek("page"))
	limitStr := string(ctx.QueryArgs().Peek("limit"))
	page, _ = strconv.Atoi(pageStr)
	if page <= 0 {
		page = 1
	}
	limit, _ = strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return page, limit
}

// HandleLogin authenticates the admin and returns a JWT token.
func (h *Handler) HandleLogin(ctx *fasthttp.RequestCtx) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}

	// Always run bcrypt comparison to avoid timing leaks
	matchedPassword := false
	if req.Username == h.cfg.Security.AdminUsername {
		if h.cfg.Security.AdminPasswordHash == "" {
			h.jsonError(ctx, fasthttp.StatusForbidden, "admin password not configured")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(h.cfg.Security.AdminPasswordHash), []byte(req.Password)) == nil {
			matchedPassword = true
		}
	} else {
		// Constant-time dummy comparison to prevent timing leak
		dummyHash := "$2a$10$000000000000000000000uGYAyOEPv8VQ8H1Vw8BrSbxWJvOXqWK"
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
	}
	if !matchedPassword {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := auth.GenerateToken(h.cfg.Security.JWTSecret, "admin", h.cfg.Security.AdminUsername, auth.TokenTypeAdmin, 24*time.Hour)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	h.jsonResponse(ctx, 200, map[string]string{"token": token})
}

// RequireAuth verifies the Bearer JWT in the Authorization header.
// Returns (authenticated bool, username string).
func (h *Handler) RequireAuth(ctx *fasthttp.RequestCtx) bool {
	_, ok := h.RequireAuthWithUser(ctx)
	return ok
}

// RequireAuthWithUser verifies the Bearer JWT and returns the username.
func (h *Handler) RequireAuthWithUser(ctx *fasthttp.RequestCtx) (string, bool) {
	authHeader := string(ctx.Request.Header.Peek("Authorization"))
	if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return "", false
	}
	tokenStr := authHeader[7:]
	claims, err := auth.ParseToken(h.cfg.Security.JWTSecret, tokenStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return "", false
	}
	if claims.Type != auth.TokenTypeAdmin {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return claims.Username, true
}

// getAdminUser extracts the admin username from the context.
func (h *Handler) getAdminUser(ctx *fasthttp.RequestCtx) string {
	if v := ctx.UserValue("admin_user"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "admin"
}

// HandleInitStatus returns whether the system has been initialized.
func (h *Handler) HandleInitStatus(ctx *fasthttp.RequestCtx) {
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"initialized": h.cfg.Initialized(),
	})
}

// HandleSetup performs the initial admin setup (username + password).
func (h *Handler) HandleSetup(ctx *fasthttp.RequestCtx) {
	// Fast path: already initialized at config level
	if h.cfg.Initialized() {
		h.jsonError(ctx, fasthttp.StatusForbidden, "already initialized")
		return
	}

	h.setupMu.Lock()
	defer h.setupMu.Unlock()

	if h.setupDone {
		h.jsonError(ctx, fasthttp.StatusForbidden, "already initialized")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Username == "" || req.Password == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "username and password are required")
		return
	}
	if len(req.Password) < 6 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	// Update in-memory config
	h.cfg.Security.AdminUsername = req.Username
	h.cfg.Security.AdminPasswordHash = string(hash)

	// Persist to config file
	if err := config.Save(h.cfg, h.cfgPath); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "save config failed")
		return
	}

	// Mark as done — subsequent calls will be rejected
	h.setupDone = true

	// Auto-login: generate JWT
	token, err := auth.GenerateToken(h.cfg.Security.JWTSecret, "admin", h.cfg.Security.AdminUsername, auth.TokenTypeAdmin, 24*time.Hour)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	h.jsonResponse(ctx, 200, map[string]interface{}{
		"token":    token,
		"username": h.cfg.Security.AdminUsername,
	})
}

// HandleDashboard returns admin dashboard statistics.
func (h *Handler) HandleDashboard(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var totalRequests int64
	var totalTokens int64
	var activeChannels int64
	var activeAccounts int64
	h.db.Model(&db.Log{}).Count(&totalRequests)
	h.db.Model(&db.Log{}).Select("COALESCE(SUM(total_tokens), 0)").Scan(&totalTokens)
	h.db.Model(&db.Channel{}).Where("deleted_at IS NULL AND enabled = ?", true).Count(&activeChannels)
	h.db.Model(&db.Account{}).Where("deleted_at IS NULL AND enabled = ?", true).Count(&activeAccounts)
	h.jsonResponse(ctx, 200, DashboardDTO{
		TotalRequests:  totalRequests,
		TotalTokens:    totalTokens,
		ActiveChannels: activeChannels,
		ActiveAccounts: activeAccounts,
	})
}
