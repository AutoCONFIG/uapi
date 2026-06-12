package admin

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/oauthprovider"
	"github.com/AutoCONFIG/uapi/internal/quota"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// QuotaScheduler is the interface for refreshing account quota data.
type QuotaScheduler interface {
	RefreshAccount(accountID uuid.UUID) (*quota.QuotaData, error)
	RefreshChannel(channelID uuid.UUID) ([]*quota.QuotaData, []error)
}

// Handler is the main admin handler that holds shared state and provides
// authentication, setup, login, and dashboard endpoints.
type Handler struct {
	db              *gorm.DB
	cfg             *config.Config
	cfgPath         string
	RefreshPool     func(channelID string)
	RemovePool      func(channelID string)
	OAuthIdle       *OAuthIdleMaintainer
	quotaScheduler  QuotaScheduler
	accountRecovery AccountRecovery
	setupMu         sync.Mutex
	setupDone       bool
	oauthMu         sync.Mutex
	oauthSessions   map[string]*oauthSession
	payloadUpdater  LargePayloadThresholdUpdater
}

type AccountRecovery interface {
	RecoverAccount(accountID, channelID string)
	HandleAccountFailure(accountID, channelID string, statusCode int, body []byte, isQuota bool)
}

type LargePayloadThresholdUpdater interface {
	SetLargePayloadThreshold(thresholdMB int)
}

func (h *Handler) SetAccountRecovery(recovery AccountRecovery) {
	h.accountRecovery = recovery
}

func (h *Handler) SetLargePayloadThresholdUpdater(updater LargePayloadThresholdUpdater) {
	h.payloadUpdater = updater
}

// NewHandler creates a new admin Handler.
func NewHandler(database *gorm.DB, cfg *config.Config, cfgPath string, refreshPool func(channelID string), removePool func(channelID string)) *Handler {
	return &Handler{
		db: database, cfg: cfg, cfgPath: cfgPath,
		RefreshPool: refreshPool, RemovePool: removePool,
		oauthSessions: make(map[string]*oauthSession),
	}
}

func (h *Handler) SetQuotaScheduler(s QuotaScheduler) {
	h.quotaScheduler = s
}

func (h *Handler) HandleChannelCatalog(ctx *fasthttp.RequestCtx) {
	oauth := make([]ChannelPresetDTO, 0)
	for _, spec := range oauthprovider.SupportedSpecs() {
		oauth = append(oauth, ChannelPresetDTO{
			ID:             spec.APIFormat,
			Label:          spec.Label,
			Type:           spec.ChannelType,
			APIFormat:      spec.APIFormat,
			Auth:           "oauth",
			Endpoint:       spec.DefaultEndpoint,
			Models:         spec.Models,
			ModelAliases:   spec.ModelAliases,
			Note:           spec.Label + " OAuth",
			ManualCallback: spec.ManualCallback,
			DeviceFlow:     spec.DeviceFlow,
			Quota:          spec.Quota,
		})
	}
	h.jsonResponse(ctx, fasthttp.StatusOK, ChannelCatalogResponse{
		OAuth: oauth,
		Reverse: []ChannelPresetDTO{
			{ID: "chatgpt_reverse", Label: "ChatGPT Reverse", Type: "openai", APIFormat: "chatgpt_reverse", Auth: "reverse", Endpoint: "https://chatgpt.com", Models: "auto,gpt-5.5,gpt-5.5-thinking,gpt-5.4,gpt-5.4-mini,gpt-5.3,gpt-5.3-mini,gpt-5-mini", Note: "ChatGPT web reverse"},
		},
		APIKey: []ChannelPresetDTO{
			{ID: "openai_responses_api", Label: "OpenAI Responses API", Type: "openai", APIFormat: "responses", Auth: "apikey", Endpoint: "https://api.openai.com/v1", Note: "OpenAI Responses API"},
			{ID: "openai_chat_completions", Label: "OpenAI Chat Completions API", Type: "openai", APIFormat: "standard", Auth: "apikey", Endpoint: "https://api.openai.com/v1", Note: "OpenAI Chat Completions API"},
			{ID: "gemini_api", Label: "Gemini API", Type: "gemini", APIFormat: "standard", Auth: "apikey", Endpoint: "https://generativelanguage.googleapis.com/v1beta", Note: "Gemini generateContent API"},
			{ID: "anthropic_messages", Label: "Anthropic Messages API", Type: "anthropic", APIFormat: "standard", Auth: "apikey", Endpoint: "https://api.anthropic.com/v1", Note: "Anthropic Messages API"},
		},
	})
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
	if limit <= 0 {
		limit = 20
	} else if limit > 1000 {
		limit = 1000
	}
	return page, limit
}

type AuthResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

func (h *Handler) authDurations() (time.Duration, time.Duration) {
	access := 15 * time.Minute
	refresh := 720 * time.Hour
	if h.cfg.Auth.AccessTokenExpiry != "" {
		if d, err := time.ParseDuration(h.cfg.Auth.AccessTokenExpiry); err == nil {
			access = d
		}
	}
	if h.cfg.Auth.RefreshTokenExpiry != "" {
		if d, err := time.ParseDuration(h.cfg.Auth.RefreshTokenExpiry); err == nil {
			refresh = d
		}
	}
	return access, refresh
}

func (h *Handler) issueAdminTokenPair() (*AuthResponse, error) {
	username, passwordHash := h.adminCredentials()
	accessExpiry, refreshExpiry := h.authDurations()
	now := time.Now()
	version := auth.SecretVersion(passwordHash)
	accessToken, err := auth.GenerateTokenWithVersion(h.cfg.Security.JWTSecret, "admin", username, auth.TokenTypeAdmin, accessExpiry, version)
	if err != nil {
		return nil, err
	}
	refreshToken, err := auth.GenerateTokenWithVersion(h.cfg.Security.JWTSecret, "admin", username, auth.TokenTypeAdminRefresh, refreshExpiry, version)
	if err != nil {
		return nil, err
	}
	return &AuthResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  now.Add(accessExpiry).Unix(),
		RefreshExpiresAt: now.Add(refreshExpiry).Unix(),
	}, nil
}

func (h *Handler) adminCredentials() (string, string) {
	username := appsettings.Get(h.db, appsettings.AdminUsername, "admin")
	if username == "" {
		username = "admin"
	}
	passwordHash := appsettings.Get(h.db, appsettings.AdminPasswordHash, "")
	return username, passwordHash
}

func (h *Handler) adminInitialized() bool {
	_, passwordHash := h.adminCredentials()
	return passwordHash != ""
}

// HandleLogin authenticates the admin and returns an access/refresh token pair.
func (h *Handler) HandleLogin(ctx *fasthttp.RequestCtx) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}

	// Always run bcrypt comparison to avoid timing leaks
	matchedPassword := false
	adminUsername, adminPasswordHash := h.adminCredentials()
	if subtle.ConstantTimeCompare([]byte(req.Email), []byte(adminUsername)) == 1 {
		if adminPasswordHash == "" {
			h.jsonError(ctx, fasthttp.StatusForbidden, "admin password not configured")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(adminPasswordHash), []byte(req.Password)) == nil {
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

	resp, err := h.issueAdminTokenPair()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	h.jsonResponse(ctx, 200, resp)
}

func (h *Handler) HandleRefresh(ctx *fasthttp.RequestCtx) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil || req.RefreshToken == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	claims, err := auth.ParseToken(h.cfg.Security.JWTSecret, req.RefreshToken)
	adminUsername, adminPasswordHash := h.adminCredentials()
	if err != nil || claims.Type != auth.TokenTypeAdminRefresh || claims.Username != adminUsername || claims.Version != auth.SecretVersion(adminPasswordHash) {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid refresh token")
		return
	}
	if adminPasswordHash == "" {
		h.jsonError(ctx, fasthttp.StatusForbidden, "admin password not configured")
		return
	}
	resp, err := h.issueAdminTokenPair()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}
	h.jsonResponse(ctx, 200, resp)
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
		"initialized": h.adminInitialized(),
	})
}

// HandleSetup performs the initial admin setup (username + password).
func (h *Handler) HandleSetup(ctx *fasthttp.RequestCtx) {
	// Fast path: already initialized at config level
	if h.adminInitialized() {
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
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Email == "" || req.Password == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "email and password are required")
		return
	}
	if len(req.Password) < 8 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	if err := appsettings.SetMany(h.db, map[string]string{
		appsettings.AdminUsername:     req.Email,
		appsettings.AdminPasswordHash: string(hash),
	}); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "save setup failed")
		return
	}

	// Mark as done — subsequent calls will be rejected
	h.setupDone = true

	resp, err := h.issueAdminTokenPair()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "internal error")
		return
	}

	h.jsonResponse(ctx, 200, resp)
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

// HandleAccountCredentialExport returns decrypted credential material to authenticated admins.
func (h *Handler) HandleAccountCredentialExport(ctx *fasthttp.RequestCtx) {
	h.exportAccountCredential(ctx)
}

// HandleRefreshAccountQuota refreshes quota for a single account.
func (h *Handler) HandleRefreshAccountQuota(ctx *fasthttp.RequestCtx) {
	idStr := ctx.UserValue("id")
	if idStr == nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "missing account id")
		return
	}
	accountID, err := uuid.Parse(fmt.Sprint(idStr))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid account id")
		return
	}
	if h.quotaScheduler == nil {
		h.jsonError(ctx, fasthttp.StatusServiceUnavailable, "quota scheduler not available")
		return
	}
	qd, err := h.quotaScheduler.RefreshAccount(accountID)
	if err != nil {
		if h.handleQuotaAuthFailure(accountID, err) {
			h.jsonError(ctx, fasthttp.StatusUnauthorized, "认证失败：账号凭证无效或已过期，已按账号异常处理")
			return
		}
		h.jsonError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if qd != nil && qd.IsForbidden {
		h.handleQuotaForbidden(accountID, qd.ForbiddenReason)
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "认证失败：账号无效或无权访问额度接口，已按账号异常处理")
		return
	}
	h.jsonResponse(ctx, fasthttp.StatusOK, qd)
}

// HandleRefreshChannelQuota refreshes quota for all OAuth accounts under a channel.
func (h *Handler) HandleRefreshChannelQuota(ctx *fasthttp.RequestCtx) {
	idStr := ctx.UserValue("id")
	if idStr == nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "missing channel id")
		return
	}
	channelID, err := uuid.Parse(fmt.Sprint(idStr))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid channel id")
		return
	}
	if h.quotaScheduler == nil {
		h.jsonError(ctx, fasthttp.StatusServiceUnavailable, "quota scheduler not available")
		return
	}
	results, errs := h.quotaScheduler.RefreshChannel(channelID)
	errorMessages := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		if h.handleQuotaAuthFailureFromError(err) {
			errorMessages = append(errorMessages, "认证失败：账号凭证无效或已过期，已按账号异常处理")
			continue
		}
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		errorMessages = append(errorMessages, msg)
	}
	authFailures := 0
	for _, qd := range results {
		if qd == nil || !qd.IsForbidden {
			continue
		}
		authFailures++
	}
	if authFailures > 0 {
		errorMessages = append(errorMessages, fmt.Sprintf("认证失败：%d 个账号无效或无权访问额度接口", authFailures))
	}
	h.jsonResponse(ctx, fasthttp.StatusOK, map[string]interface{}{
		"refreshed":      len(results),
		"errors":         len(errs) + authFailures,
		"error_messages": errorMessages,
	})
}

func (h *Handler) handleQuotaAuthFailure(accountID uuid.UUID, err error) bool {
	if !quota.IsAuthFailureError(err) {
		return false
	}
	h.applyQuotaAccountFailure(accountID, fasthttp.StatusUnauthorized, []byte("credential_invalid: "+err.Error()), false)
	return true
}

func (h *Handler) handleQuotaAuthFailureFromError(err error) bool {
	var accountErr *quota.AccountError
	if !quota.AsAccountError(err, &accountErr) {
		return false
	}
	return h.handleQuotaAuthFailure(accountErr.AccountID, accountErr.Err)
}

func (h *Handler) handleQuotaForbidden(accountID uuid.UUID, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "account_forbidden"
	}
	h.applyQuotaAccountFailure(accountID, fasthttp.StatusUnauthorized, []byte("credential_invalid: "+reason), false)
}

func (h *Handler) applyQuotaAccountFailure(accountID uuid.UUID, statusCode int, body []byte, isQuota bool) {
	if h == nil || h.db == nil || accountID == uuid.Nil {
		return
	}
	var acc db.Account
	if err := h.db.Where("id = ? AND deleted_at IS NULL", accountID).First(&acc).Error; err != nil {
		return
	}
	if h.accountRecovery != nil {
		h.accountRecovery.HandleAccountFailure(acc.ID.String(), acc.ChannelID.String(), statusCode, body, isQuota)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if acc.Metadata == nil {
		acc.Metadata = map[string]interface{}{}
	}
	acc.Metadata["disabled_reason"] = "credential_invalid"
	acc.Metadata["disabled_at"] = now
	acc.Metadata["auth_failure_next_action"] = "disabled"
	acc.Metadata["auto_disable_reason"] = "credential_invalid"
	acc.Metadata["auto_disable_time"] = now
	acc.Metadata["last_terminal_error_reason"] = "credential_invalid"
	acc.Metadata["last_terminal_error_at"] = now
	acc.Enabled = false
	acc.CooldownUntil = nil
	_ = h.db.Model(&db.Account{}).
		Where("id = ? AND deleted_at IS NULL", acc.ID).
		Updates(map[string]interface{}{
			"enabled":        false,
			"cooldown_until": nil,
			"metadata":       acc.Metadata,
		}).Error
	if h.RefreshPool != nil {
		h.RefreshPool(acc.ChannelID.String())
	}
}
