package admin

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/auth"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/crypto"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Handler struct {
	db       *gorm.DB
	cfg      *config.Config
	cfgPath  string
}

func NewHandler(database *gorm.DB, cfg *config.Config, cfgPath string) *Handler {
	return &Handler{db: database, cfg: cfg, cfgPath: cfgPath}
}

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

func (h *Handler) jsonError(ctx *fasthttp.RequestCtx, status int, msg string) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(status)
	body, _ := json.Marshal(map[string]interface{}{
		"code":    status,
		"message": msg,
	})
	ctx.SetBody(body)
}

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

	if req.Username != h.cfg.Security.AdminUsername {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid credentials")
		return
	}

	if h.cfg.Security.AdminPasswordHash == "" {
		h.jsonError(ctx, fasthttp.StatusForbidden, "admin password not configured")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h.cfg.Security.AdminPasswordHash), []byte(req.Password)); err != nil {
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
func (h *Handler) RequireAuth(ctx *fasthttp.RequestCtx) bool {
	authHeader := string(ctx.Request.Header.Peek("Authorization"))
	if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return false
	}
	tokenStr := authHeader[7:]
	if _, err := auth.ParseToken(tokenStr, h.cfg.Security.JWTSecret); err != nil {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

// --- Channels ---

func (h *Handler) HandleChannels(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listChannels(ctx)
	case "POST":
		h.createChannel(ctx)
	case "PUT":
		h.updateChannel(ctx)
	case "DELETE":
		h.deleteChannel(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listChannels(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Channel
	h.db.Model(&db.Channel{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": items,
	})
}

func (h *Handler) createChannel(ctx *fasthttp.RequestCtx) {
	var req db.Channel
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Type == "" || req.Endpoint == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name, type and endpoint are required")
		return
	}
	req.ID = uuid.New()
	if err := h.db.Create(&req).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	h.jsonResponse(ctx, 200, req)
}

func (h *Handler) updateChannel(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req db.Channel
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Type != "" {
		updates["type"] = req.Type
	}
	if req.Endpoint != "" {
		updates["endpoint"] = req.Endpoint
	}
	updates["enabled"] = req.Enabled
	if req.Models != "" {
		updates["models"] = req.Models
	}
	updates["priority"] = req.Priority
	updates["api_format"] = req.APIFormat
	updates["force_stream"] = req.ForceStream
	updates["affinity_ttl"] = req.AffinityTTL
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteChannel(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	if err := h.db.Model(&db.Channel{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

// --- Accounts ---

func (h *Handler) HandleAccounts(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listAccounts(ctx)
	case "POST":
		h.createAccount(ctx)
	case "PUT":
		h.updateAccount(ctx)
	case "DELETE":
		h.deleteAccount(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listAccounts(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Account
	h.db.Model(&db.Account{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": items,
	})
}

func (h *Handler) createAccount(ctx *fasthttp.RequestCtx) {
	var req struct {
		ChannelID   uuid.UUID `json:"channel_id"`
		Name        string    `json:"name"`
		Credentials string    `json:"credentials"`
		Weight      int       `json:"weight"`
		Enabled     bool      `json:"enabled"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.ChannelID == uuid.Nil || req.Credentials == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name, channel_id and credentials are required")
		return
	}
	encrypted, err := crypto.Encrypt(req.Credentials)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
		return
	}
	acc := db.Account{
		ChannelID:   req.ChannelID,
		Name:        req.Name,
		Credentials: encrypted,
		Weight:      req.Weight,
		Enabled:     req.Enabled,
	}
	acc.ID = uuid.New()
	if err := h.db.Create(&acc).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	h.jsonResponse(ctx, 200, acc)
}

func (h *Handler) updateAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ChannelID     uuid.UUID  `json:"channel_id"`
		Name          string     `json:"name"`
		Credentials   string     `json:"credentials"`
		Weight        int        `json:"weight"`
		Enabled       bool       `json:"enabled"`
		CooldownUntil *time.Time `json:"cooldown_until"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Account
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ChannelID != uuid.Nil {
		updates["channel_id"] = req.ChannelID
	}
	if req.Credentials != "" {
		encrypted, err := crypto.Encrypt(req.Credentials)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
			return
		}
		updates["credentials"] = encrypted
	}
	updates["weight"] = req.Weight
	updates["enabled"] = req.Enabled
	if req.CooldownUntil != nil {
		updates["cooldown_until"] = req.CooldownUntil
	} else {
		updates["cooldown_until"] = nil
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	if err := h.db.Model(&db.Account{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

// --- Tokens ---

func (h *Handler) HandleTokens(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listTokens(ctx)
	case "POST":
		h.createToken(ctx)
	case "PUT":
		h.updateToken(ctx)
	case "DELETE":
		h.deleteToken(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listTokens(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Token
	h.db.Model(&db.Token{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": items,
	})
}

func (h *Handler) createToken(ctx *fasthttp.RequestCtx) {
	var req db.Token
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Key == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name and key are required")
		return
	}
	req.ID = uuid.New()
	if err := h.db.Create(&req).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	h.jsonResponse(ctx, 200, req)
}

func (h *Handler) updateToken(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req db.Token
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Token
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Key != "" {
		updates["key"] = req.Key
	}
	updates["enabled"] = req.Enabled
	if req.IPWhitelist != "" {
		updates["ip_whitelist"] = req.IPWhitelist
	}
	updates["unlimited"] = req.Unlimited
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteToken(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	if err := h.db.Model(&db.Token{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

// --- Plans ---

func (h *Handler) HandlePlans(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listPlans(ctx)
	case "POST":
		h.createPlan(ctx)
	case "PUT":
		h.updatePlan(ctx)
	case "DELETE":
		h.deletePlan(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listPlans(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Plan
	h.db.Model(&db.Plan{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": items,
	})
}

func (h *Handler) createPlan(ctx *fasthttp.RequestCtx) {
	var req db.Plan
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Type == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name and type are required")
		return
	}
	req.ID = uuid.New()
	if err := h.db.Create(&req).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	h.jsonResponse(ctx, 200, req)
}

func (h *Handler) updatePlan(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req db.Plan
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Plan
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Type != "" {
		updates["type"] = req.Type
	}
	updates["limits"] = req.Limits
	updates["model_ratios"] = req.ModelRatios
	updates["completion_ratio"] = req.CompletionRatio
	updates["token_quota"] = req.TokenQuota
	updates["enabled"] = req.Enabled
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deletePlan(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	if err := h.db.Model(&db.Plan{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

// --- Logs ---

func (h *Handler) HandleLogs(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Log
	h.db.Model(&db.Log{}).Count(&total)
	h.db.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": items,
	})
}

// --- Setup / Init ---

func (h *Handler) HandleInitStatus(ctx *fasthttp.RequestCtx) {
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"initialized": h.cfg.Initialized(),
	})
}

func (h *Handler) HandleSetup(ctx *fasthttp.RequestCtx) {
	// Already initialized — reject
	if h.cfg.Initialized() {
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

	// Auto-login: generate JWT
	token, err := auth.GenerateToken(h.cfg.Security.JWTSecret, "admin", h.cfg.Security.AdminUsername, auth.TokenTypeAdmin, 24*time.Hour)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	h.jsonResponse(ctx, 200, map[string]interface{}{
		"token":     token,
		"username":  h.cfg.Security.AdminUsername,
	})
}

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
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"total_requests":  totalRequests,
		"total_tokens":    totalTokens,
		"active_channels": activeChannels,
		"active_accounts": activeAccounts,
	})
}
