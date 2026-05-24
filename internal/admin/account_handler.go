package admin

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
)

// HandleAccounts routes account CRUD operations by HTTP method.
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
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createAccount(ctx *fasthttp.RequestCtx) {
	var req CreateAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.ChannelID == uuid.Nil || req.Credentials == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name, channel_id and credentials are required")
		return
	}
	var ch db.Channel
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", req.ChannelID).First(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
		return
	}
	if isCodeAPIFormat(ch.APIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "Code channels require OAuth credentials")
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
		Endpoint:    accountEndpointOrDefault(ch, req.Endpoint),
		Weight:      req.Weight,
		Enabled:     true,
	}
	acc.ID = uuid.New()
	if err := h.db.Create(&acc).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(acc.ChannelID.String())
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&acc)
	}
	auditCreate(h.db, "account", acc.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, acc)
}

func isCodeAPIFormat(format string) bool {
	return format == "codex" || format == "gemini_code" || format == "claude_code"
}

func oauthAccountMatchesCodeChannel(acc db.Account, ch db.Channel) bool {
	if acc.CredType != "oauth_token" {
		return false
	}
	switch ch.APIFormat {
	case "codex":
		return tokenURLIs(acc.TokenURL, "https://auth.openai.com/oauth/token")
	case "gemini_code":
		return tokenURLIs(acc.TokenURL, "https://oauth2.googleapis.com/token")
	case "claude_code":
		return tokenURLIs(acc.TokenURL, "https://platform.claude.com/v1/oauth/token")
	default:
		return true
	}
}

func codeOAuthAccountRequiresCodeChannel(acc db.Account) bool {
	if acc.CredType != "oauth_token" {
		return false
	}
	return tokenURLIs(acc.TokenURL, "https://auth.openai.com/oauth/token") ||
		tokenURLIs(acc.TokenURL, "https://oauth2.googleapis.com/token") ||
		tokenURLIs(acc.TokenURL, "https://platform.claude.com/v1/oauth/token")
}

func tokenURLIs(rawURL, expectedURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	expected, err := url.Parse(expectedURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, expected.Scheme) &&
		strings.EqualFold(parsed.Hostname(), expected.Hostname()) &&
		parsed.Port() == "" &&
		parsed.EscapedPath() == expected.EscapedPath() &&
		parsed.RawQuery == "" &&
		parsed.Fragment == ""
}

func (h *Handler) exportAccountCredential(ctx *fasthttp.RequestCtx) {
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil || req.ID == "" || req.Password == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "id and password are required")
		return
	}
	if h.cfg.Security.AdminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(h.cfg.Security.AdminPasswordHash), []byte(req.Password)) != nil {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid password")
		return
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var acc db.Account
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&acc).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	var ch db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", acc.ChannelID).First(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	credential, err := crypto.Decrypt(acc.Credentials)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "decrypt credential failed")
		return
	}
	data := map[string]interface{}{
		"name":     acc.Name,
		"provider": ch.Type,
		"endpoint": upstreamconfig.AccountEndpoint(&ch, &acc),
	}
	if ch.APIFormat != "" {
		data["api_format"] = ch.APIFormat
	}
	if acc.CredType == "oauth_token" {
		data["type"] = "oauth_token"
		data["access_token"] = credential
		refreshToken := ""
		if acc.RefreshToken != "" {
			refreshToken, err = crypto.Decrypt(acc.RefreshToken)
			if err != nil {
				h.jsonError(ctx, fasthttp.StatusInternalServerError, "decrypt refresh token failed")
				return
			}
		}
		clientSecret := ""
		if acc.ClientSecret != "" {
			clientSecret, err = crypto.Decrypt(acc.ClientSecret)
			if err != nil {
				h.jsonError(ctx, fasthttp.StatusInternalServerError, "decrypt client secret failed")
				return
			}
		}
		if refreshToken != "" {
			data["refresh_token"] = refreshToken
		}
		if acc.TokenExpiry != nil {
			data["expiry"] = acc.TokenExpiry
		}
		if acc.ClientID != "" {
			data["client_id"] = acc.ClientID
		}
		if clientSecret != "" {
			data["client_secret"] = clientSecret
		}
		if acc.TokenURL != "" {
			data["token_url"] = acc.TokenURL
		}
	} else {
		data["type"] = "api_key"
		data["api_key"] = credential
	}
	auditCreate(h.db, "account_export", acc.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, data)
}

func (h *Handler) updateAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Account
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	originalChannelID := existing.ChannelID
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ChannelID != uuid.Nil {
		var target db.Channel
		if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", req.ChannelID).First(&target).Error; err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
			return
		}
		if isCodeAPIFormat(target.APIFormat) && existing.CredType != "oauth_token" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "Code channels require OAuth credentials")
			return
		}
		if codeOAuthAccountRequiresCodeChannel(existing) && !isCodeAPIFormat(target.APIFormat) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "Code OAuth credentials can only be assigned to Code channels")
			return
		}
		if isCodeAPIFormat(target.APIFormat) {
			if !oauthAccountMatchesCodeChannel(existing, target) {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth credential provider does not match Code channel")
				return
			}
		}
		updates["channel_id"] = req.ChannelID
	}
	if req.Credentials != "" {
		currentChannelID := existing.ChannelID
		if req.ChannelID != uuid.Nil {
			currentChannelID = req.ChannelID
		}
		var target db.Channel
		if err := h.db.Where("id = ? AND deleted_at IS NULL", currentChannelID).First(&target).Error; err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
			return
		}
		if isCodeAPIFormat(target.APIFormat) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "Code channel credentials must be updated through OAuth")
			return
		}
		encrypted, err := crypto.Encrypt(req.Credentials)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
			return
		}
		updates["credentials"] = encrypted
	}
	if req.Endpoint != nil {
		endpoint := strings.TrimSpace(*req.Endpoint)
		if endpoint == "" {
			currentChannelID := existing.ChannelID
			if req.ChannelID != uuid.Nil {
				currentChannelID = req.ChannelID
			}
			var target db.Channel
			if err := h.db.Where("id = ? AND deleted_at IS NULL", currentChannelID).First(&target).Error; err != nil {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
				return
			}
			endpoint = upstreamconfig.DefaultEndpoint(target.Type, target.APIFormat)
		}
		updates["endpoint"] = endpoint
	}
	if req.Weight != nil {
		updates["weight"] = *req.Weight
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.CooldownUntil != nil {
		updates["cooldown_until"] = req.CooldownUntil
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	if h.RefreshPool != nil {
		if originalChannelID != existing.ChannelID {
			// Channel changed: refresh both old and new channel pools
			h.RefreshPool(originalChannelID.String())
			h.RefreshPool(existing.ChannelID.String())
		} else {
			h.RefreshPool(existing.ChannelID.String())
		}
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&existing)
	}
	auditUpdate(h.db, "account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func accountEndpointOrDefault(ch db.Channel, endpoint string) string {
	if value := strings.TrimSpace(endpoint); value != "" {
		return value
	}
	return upstreamconfig.DefaultEndpoint(ch.Type, ch.APIFormat)
}

func (h *Handler) deleteAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.Account{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.CancelAccount(id)
	}
	if h.RefreshPool != nil {
		// Find the channel this account belonged to so we can refresh its pool
		var acc db.Account
		if h.db.Unscoped().Where("id = ?", id).First(&acc).Error == nil {
			h.RefreshPool(acc.ChannelID.String())
		}
	}
	auditDelete(h.db, "account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
