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
	if isOAuthAPIFormat(ch.APIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth channels require OAuth credentials")
		return
	}
	credType := "api_key"
	if isReverseAPIFormat(ch.APIFormat) {
		credType = "chatgpt_reverse"
	}
	credentialValue := req.Credentials
	var refreshToken string
	var tokenExpiry *time.Time
	if credType == "chatgpt_reverse" {
		credentialValue, refreshToken, tokenExpiry = parseReverseCredentialInput(req.Credentials)
	}
	encrypted, err := crypto.Encrypt(credentialValue)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
		return
	}
	acc := db.Account{
		ChannelID:   req.ChannelID,
		Name:        req.Name,
		Credentials: encrypted,
		CredType:    credType,
		Endpoint:    accountEndpointOrDefault(ch, req.Endpoint),
		Weight:      req.Weight,
		Enabled:     true,
	}
	if refreshToken != "" {
		encryptedRefresh, err := crypto.Encrypt(refreshToken)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt refresh token failed")
			return
		}
		acc.RefreshToken = encryptedRefresh
		acc.TokenURL = "https://auth.openai.com/oauth/token"
		acc.TokenExpiry = tokenExpiry
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
	auditCreateCtx(h.db, "account", acc.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": acc.Name, "channel_id": acc.ChannelID, "endpoint": acc.Endpoint, "weight": acc.Weight})
	h.jsonResponse(ctx, 200, acc)
}

func isOAuthAPIFormat(format string) bool {
	return format == "codex" || format == "gemini_code" || format == "claude_code" || format == "antigravity"
}

func isReverseAPIFormat(format string) bool {
	return format == "chatgpt_reverse"
}

func reverseAccountRequiresReverseChannel(acc db.Account) bool {
	return acc.CredType == "chatgpt_reverse"
}

func parseReverseCredentialInput(raw string) (accessToken string, refreshToken string, expiry *time.Time) {
	raw = strings.TrimSpace(raw)
	accessToken = raw
	if !strings.HasPrefix(raw, "{") {
		return accessToken, "", nil
	}
	var payload struct {
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		Expiry       interface{} `json:"expiry"`
		ExpiresAt    interface{} `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return accessToken, "", nil
	}
	if strings.TrimSpace(payload.AccessToken) != "" {
		accessToken = strings.TrimSpace(payload.AccessToken)
	}
	refreshToken = strings.TrimSpace(payload.RefreshToken)
	if parsed := parseCredentialExpiry(payload.Expiry); parsed != nil {
		expiry = parsed
	} else if parsed := parseCredentialExpiry(payload.ExpiresAt); parsed != nil {
		expiry = parsed
	}
	return accessToken, refreshToken, expiry
}

func parseCredentialExpiry(value interface{}) *time.Time {
	switch v := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
			return &parsed
		}
	case float64:
		if v > 0 {
			parsed := time.Unix(int64(v), 0)
			return &parsed
		}
	}
	return nil
}

func oauthAccountMatchesOAuthChannel(acc db.Account, ch db.Channel) bool {
	if acc.CredType != "oauth_token" {
		return false
	}
	switch ch.APIFormat {
	case "codex":
		return tokenURLIs(acc.TokenURL, "https://auth.openai.com/oauth/token")
	case "gemini_code":
		return tokenURLIs(acc.TokenURL, "https://oauth2.googleapis.com/token") && accountOAuthProvider(acc) != "antigravity"
	case "claude_code":
		return tokenURLIs(acc.TokenURL, "https://platform.claude.com/v1/oauth/token")
	case "antigravity":
		return accountOAuthProvider(acc) == "antigravity"
	default:
		return true
	}
}

func oauthAccountRequiresOAuthChannel(acc db.Account) bool {
	if acc.CredType != "oauth_token" {
		return false
	}
	return tokenURLIs(acc.TokenURL, "https://auth.openai.com/oauth/token") ||
		tokenURLIs(acc.TokenURL, "https://oauth2.googleapis.com/token") ||
		tokenURLIs(acc.TokenURL, "https://platform.claude.com/v1/oauth/token")
}

func accountOAuthProvider(acc db.Account) string {
	if acc.Metadata != nil {
		if provider, ok := acc.Metadata["oauth_provider"].(string); ok && strings.TrimSpace(provider) != "" {
			return strings.ToLower(strings.TrimSpace(provider))
		}
	}
	switch {
	case tokenURLIs(acc.TokenURL, "https://auth.openai.com/oauth/token"):
		return "openai"
	case tokenURLIs(acc.TokenURL, "https://platform.claude.com/v1/oauth/token"):
		return "anthropic"
	case tokenURLIs(acc.TokenURL, "https://oauth2.googleapis.com/token"):
		return "gemini"
	default:
		return ""
	}
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
	_, adminPasswordHash := h.adminCredentials()
	if adminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(adminPasswordHash), []byte(req.Password)) != nil {
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
		if ch.APIFormat == "codex" {
			if refreshToken == "" {
				h.jsonError(ctx, fasthttp.StatusInternalServerError, "codex account has no refresh token")
				return
			}
			authJSON, err := exportCodexAuthJSON(&acc, credential, refreshToken)
			if err != nil {
				h.jsonError(ctx, fasthttp.StatusInternalServerError, err.Error())
				return
			}
			auditCreateCtx(h.db, "account_export", acc.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"account_id": acc.ID, "name": acc.Name})
			h.jsonResponse(ctx, 200, authJSON)
			return
		}
		data["type"] = "oauth_token"
		data["access_token"] = credential
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
	} else if acc.CredType == "chatgpt_reverse" {
		refreshToken := ""
		if acc.RefreshToken != "" {
			refreshToken, err = crypto.Decrypt(acc.RefreshToken)
			if err != nil {
				h.jsonError(ctx, fasthttp.StatusInternalServerError, "decrypt refresh token failed")
				return
			}
		}
		data["type"] = "chatgpt_reverse"
		data["access_token"] = credential
		if refreshToken != "" {
			data["refresh_token"] = refreshToken
		}
		if acc.TokenExpiry != nil {
			data["expiry"] = acc.TokenExpiry
		}
	} else {
		data["type"] = "api_key"
		data["api_key"] = credential
	}
	auditCreateCtx(h.db, "account_export", acc.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"account_id": acc.ID, "name": acc.Name})
	h.jsonResponse(ctx, 200, data)
}

func exportCodexAuthJSON(acc *db.Account, accessToken, refreshToken string) (map[string]interface{}, error) {
	metadata := normalizeCodexAccountMetadata(acc.Metadata)
	accountID := openAIAccountID(metadata)
	if accountID == "" {
		return nil, errCodexExport("codex account metadata missing account_id")
	}
	idToken := metadataStringFromMap(metadata, "raw_id_token")
	if idToken == "" {
		return nil, errCodexExport("codex account metadata missing id_token")
	}
	return map[string]interface{}{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"last_refresh":   time.Now().UTC().Format(time.RFC3339),
		"tokens": map[string]interface{}{
			"id_token":      idToken,
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"account_id":    accountID,
		},
	}, nil
}

type codexExportError string

func (e codexExportError) Error() string { return string(e) }

func errCodexExport(message string) error { return codexExportError(message) }

func metadataStringFromMap(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
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
		if isOAuthAPIFormat(target.APIFormat) && existing.CredType != "oauth_token" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth channels require OAuth credentials")
			return
		}
		if isReverseAPIFormat(target.APIFormat) && existing.CredType != "chatgpt_reverse" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "Reverse channels require reverse credentials")
			return
		}
		if oauthAccountRequiresOAuthChannel(existing) && !isOAuthAPIFormat(target.APIFormat) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth credentials can only be assigned to OAuth channels")
			return
		}
		if reverseAccountRequiresReverseChannel(existing) && !isReverseAPIFormat(target.APIFormat) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "Reverse credentials can only be assigned to reverse channels")
			return
		}
		if isOAuthAPIFormat(target.APIFormat) {
			if !oauthAccountMatchesOAuthChannel(existing, target) {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth credential provider does not match OAuth channel")
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
		if isOAuthAPIFormat(target.APIFormat) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "OAuth channel credentials must be updated through OAuth")
			return
		}
		credentialValue := req.Credentials
		refreshToken := ""
		var tokenExpiry *time.Time
		if isReverseAPIFormat(target.APIFormat) {
			credentialValue, refreshToken, tokenExpiry = parseReverseCredentialInput(req.Credentials)
		}
		encrypted, err := crypto.Encrypt(credentialValue)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
			return
		}
		updates["credentials"] = encrypted
		if isReverseAPIFormat(target.APIFormat) {
			if refreshToken != "" {
				encryptedRefresh, err := crypto.Encrypt(refreshToken)
				if err != nil {
					h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt refresh token failed")
					return
				}
				updates["refresh_token"] = encryptedRefresh
				updates["token_url"] = "https://auth.openai.com/oauth/token"
			}
			if tokenExpiry != nil {
				updates["token_expiry"] = tokenExpiry
			}
		}
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
		if *req.Enabled {
			updates["cooldown_until"] = nil
			metadata := cloneMetadata(existing.Metadata)
			clearAccountFailureMetadata(metadata)
			updates["metadata"] = metadata
		}
	}
	if req.CooldownUntil != nil && !(req.Enabled != nil && *req.Enabled) {
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
	if req.Enabled != nil && *req.Enabled && h.accountRecovery != nil {
		h.accountRecovery.RecoverAccount(existing.ID.String(), existing.ChannelID.String())
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&existing)
	}
	auditUpdateCtx(h.db, "account", id, h.getAdminUser(ctx), ctx, updates)
	h.jsonResponse(ctx, 200, existing)
}

func cloneMetadata(metadata map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		out[k] = v
	}
	return out
}

func clearAccountFailureMetadata(metadata map[string]interface{}) {
	delete(metadata, "disabled_reason")
	delete(metadata, "disabled_at")
	delete(metadata, "auto_disable_reason")
	delete(metadata, "auto_disable_time")
	delete(metadata, "last_terminal_error_reason")
	delete(metadata, "last_terminal_error_at")
	delete(metadata, "last_terminal_error_status_code")
	delete(metadata, "last_terminal_error_channel_id")
	delete(metadata, "auth_failure_attempts")
	delete(metadata, "auth_failure_reason")
	delete(metadata, "auth_failure_at")
	delete(metadata, "auth_failure_status_code")
	delete(metadata, "auth_failure_next_action")
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
	auditDeleteCtx(h.db, "account", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
