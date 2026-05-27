package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	internalcrypto "github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthprovider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const oauthSessionTTL = 15 * time.Minute

type oauthSession struct {
	State          string
	Provider       string
	ChannelID      uuid.UUID
	AccountName    string
	ClientID       string
	ClientSecret   string
	TokenURL       string
	RedirectURI    string
	CodeVerifier   string
	Status         string
	Error          string
	Credential     string
	RefreshToken   string
	Expiry         *time.Time
	Metadata       map[string]interface{}
	CreatedAt      time.Time
	CompletedAt    *time.Time
	BoundAccountID *uuid.UUID
	CreatedBy      string // Admin username who started this OAuth flow
	CreatedByIP    string // IP address of the admin who started this OAuth flow
}

func (h *Handler) StartOAuth(ctx *fasthttp.RequestCtx) {
	adminUsername, ok := ctx.UserValue("admin_user").(string)
	if !ok || adminUsername == "" {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "unauthorized")
		return
	}
	var req StartOAuthRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	// Set admin username from JWT claims (not from request body)
	req.AdminUsername = adminUsername
	if req.ChannelID == uuid.Nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel_id is required")
		return
	}

	var channel db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", req.ChannelID).First(&channel).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	provider := strings.ToLower(req.Provider)
	if provider == "" {
		if matched, ok := oauthprovider.MatchChannel(channel); ok {
			provider = matched.Key()
		} else {
			provider = strings.ToLower(channel.Type)
		}
	}
	oauthProv, ok := oauthprovider.Get(provider)
	if !ok {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "unsupported oauth provider")
		return
	}
	provider = oauthProv.Key()
	if !oauthProv.ChannelAllowed(channel) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth login is only supported on the matching OAuth channel format")
		return
	}

	state, err := randomURLToken(32)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create state failed")
		return
	}
	verifier, challenge, err := oauthPKCE(provider)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create pkce failed")
		return
	}

	clientID := strings.TrimSpace(req.ClientID)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	tokenURL := strings.TrimSpace(req.TokenURL)
	if clientID == "" {
		clientID = oauthProv.DefaultClientID()
	}
	if clientSecret == "" {
		clientSecret = oauthProv.DefaultClientSecret()
	}
	if tokenURL == "" {
		tokenURL = oauthProv.DefaultTokenURL()
	}
	if !oauthProv.TokenURLAllowed(tokenURL) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth token_url does not match provider")
		return
	}

	if provider == "codex" && strings.EqualFold(req.Mode, "device") {
		device, err := openai.StartDeviceAuth(clientID)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusBadGateway, sanitizeOAuthError(err))
			return
		}
		session := &oauthSession{
			State: state, Provider: provider, ChannelID: req.ChannelID,
			AccountName: strings.TrimSpace(req.AccountName),
			ClientID:    clientID, ClientSecret: clientSecret,
			TokenURL: tokenURL, RedirectURI: openai.DeviceRedirectURI,
			Status: "pending", CreatedAt: time.Now(),
			CreatedBy: req.AdminUsername, CreatedByIP: ctx.RemoteIP().String(),
		}
		h.oauthMu.Lock()
		h.pruneOAuthSessionsLocked()
		h.oauthSessions[state] = session
		h.oauthMu.Unlock()

		go h.pollOpenAIDeviceOAuth(state, device.DeviceAuthID, device.UserCode, device.Interval)
		h.jsonResponse(ctx, 200, OAuthAuthURLResponse{
			AuthURL:     openai.DeviceAuthURL,
			State:       state,
			RedirectURI: openai.DeviceRedirectURI,
			Mode:        "device",
			UserCode:    device.UserCode,
			ExpiresAt:   session.CreatedAt.Add(oauthSessionTTL),
		})
		return
	}

	redirectURI := h.oauthRedirectURI(ctx)
	mode := "browser"
	if oauthProv.ManualCallback() {
		redirectURI = oauthProv.DefaultRedirectURI()
		mode = "manual_callback"
	}
	if provider == "gemini" {
		redirectURI = geminiLoopbackRedirectURI()
		verifier = ""
		challenge = ""
	}
	authURL := buildProviderAuthURL(provider, clientID, redirectURI, challenge, state)

	session := &oauthSession{
		State: state, Provider: provider, ChannelID: req.ChannelID,
		AccountName: strings.TrimSpace(req.AccountName),
		ClientID:    clientID, ClientSecret: clientSecret,
		TokenURL: tokenURL, RedirectURI: redirectURI, CodeVerifier: verifier,
		Status: "pending", CreatedAt: time.Now(),
		CreatedBy: req.AdminUsername, CreatedByIP: ctx.RemoteIP().String(),
	}
	h.oauthMu.Lock()
	h.pruneOAuthSessionsLocked()
	h.oauthSessions[state] = session
	h.oauthMu.Unlock()

	logger.Infof("admin.oauth", "oauth session started", logger.F("provider", provider), logger.F("channel_id", req.ChannelID.String()), logger.F("mode", mode))
	h.jsonResponse(ctx, 200, OAuthAuthURLResponse{
		AuthURL:     authURL,
		State:       state,
		RedirectURI: redirectURI,
		Mode:        mode,
		ExpiresAt:   session.CreatedAt.Add(oauthSessionTTL),
	})
}

func (h *Handler) CompleteOAuth(ctx *fasthttp.RequestCtx) {
	var req CompleteOAuthRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	state := strings.TrimSpace(req.State)
	code := strings.TrimSpace(req.Code)
	providerErr := ""
	var imported *oauthJSONImport
	if strings.TrimSpace(req.OAuthJSON) != "" {
		parsed, err := parseOAuthJSON(req.OAuthJSON)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, err.Error())
			return
		}
		imported = parsed
		if state == "" {
			state = imported.State
		}
		if code == "" {
			code = imported.Code
		}
		providerErr = imported.Error
	}
	if req.CallbackURL != "" {
		parsed, err := url.Parse(strings.TrimSpace(req.CallbackURL))
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid callback_url")
			return
		}
		values := parsed.Query()
		if code == "" {
			code = values.Get("code")
		}
		if state == "" {
			state = values.Get("state")
		}
		providerErr = values.Get("error")
	}
	if state == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "state is required")
		return
	}
	session := h.getOAuthSession(state)
	if session == nil {
		// When importing OAuth JSON without a prior StartOAuth call, create a
		// transient session from the request's channel_id and provider fields.
		if imported == nil || (imported.AccessToken == "" && imported.IDToken == "") {
			h.jsonError(ctx, fasthttp.StatusNotFound, "oauth session not found")
			return
		}
		if req.ChannelID == uuid.Nil || strings.TrimSpace(req.Provider) == "" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "channel_id and provider are required for JSON import without a prior OAuth session")
			return
		}
		provider := strings.ToLower(strings.TrimSpace(req.Provider))
		oauthProv, ok := oauthprovider.Get(provider)
		if !ok {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "unsupported oauth provider")
			return
		}
		provider = oauthProv.Key()
		var channel db.Channel
		if err := h.db.Where("id = ? AND deleted_at IS NULL", req.ChannelID).First(&channel).Error; err != nil {
			h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
			return
		}
		if !oauthProv.ChannelAllowed(channel) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth login is only supported on the matching OAuth channel format")
			return
		}
		adminUsername, _ := ctx.UserValue("admin_user").(string)
		session = &oauthSession{
			State: state, Provider: provider, ChannelID: req.ChannelID,
			TokenURL: oauthProv.DefaultTokenURL(), ClientID: oauthProv.DefaultClientID(),
			RedirectURI: oauthProv.DefaultRedirectURI(),
			Status:      "pending", CreatedAt: time.Now(),
			CreatedBy: adminUsername, CreatedByIP: ctx.RemoteIP().String(),
		}
		h.oauthMu.Lock()
		h.pruneOAuthSessionsLocked()
		h.oauthSessions[state] = session
		h.oauthMu.Unlock()
		logger.Infof("admin.oauth", "oauth session created from JSON import", logger.F("provider", provider), logger.F("channel_id", req.ChannelID.String()))
	}
	if imported != nil {
		h.applyOAuthJSONImport(state, imported)
		session = h.getOAuthSession(state)
		if session == nil {
			h.jsonError(ctx, fasthttp.StatusNotFound, "oauth session not found")
			return
		}
		if imported.AccessToken != "" || imported.IDToken != "" {
			// JSON import is supported for Google OAuth, Codex, and Antigravity.
			if session.Provider != "gemini" && session.Provider != "antigravity" && session.Provider != "codex" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth token JSON import is only supported for Google OAuth, Codex, and Antigravity channels")
				return
			}
			if imported.AccessToken == "" || imported.RefreshToken == "" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth_json requires access_token and refresh_token")
				return
			}
			credential := imported.AccessToken
			metadata := importedMetadata(imported)
			metadata["oauth_provider"] = session.Provider
			h.completeOAuthSession(state, credential, imported.RefreshToken, imported.Expiry, metadata, "")
			logger.Infof("admin.oauth", "oauth json import completed", logger.F("provider", session.Provider), logger.F("channel_id", session.ChannelID.String()), logger.F("has_refresh_token", imported.RefreshToken != ""), logger.F("has_expiry", imported.Expiry != nil))
			h.jsonResponse(ctx, 200, h.oauthStatusDTO(h.getOAuthSession(state)))
			return
		}
	}
	if providerErr != "" {
		h.completeOAuthSession(state, "", "", nil, nil, providerErr)
		h.jsonError(ctx, fasthttp.StatusBadRequest, providerErr)
		return
	}
	if code == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "authorization code is required")
		return
	}
	credential, refreshToken, expiry, metadata, err := exchangeOAuthCode(session, code)
	if err != nil {
		safeErr := sanitizeOAuthError(err)
		h.completeOAuthSession(state, "", "", nil, nil, safeErr)
		logger.Warnf("admin.oauth", "oauth code exchange failed", logger.F("provider", session.Provider), logger.F("channel_id", session.ChannelID.String()), logger.F("error", safeErr))
		h.jsonError(ctx, fasthttp.StatusBadGateway, safeErr)
		return
	}
	h.completeOAuthSession(state, credential, refreshToken, expiry, metadata, "")
	logger.Infof("admin.oauth", "oauth code exchange completed", logger.F("provider", session.Provider), logger.F("channel_id", session.ChannelID.String()), logger.F("has_refresh_token", refreshToken != ""), logger.F("has_expiry", expiry != nil))
	h.jsonResponse(ctx, 200, h.oauthStatusDTO(h.getOAuthSession(state)))
}

func channelAllowsOAuthProvider(channel db.Channel, provider string) bool {
	oauthProv, ok := oauthprovider.Get(provider)
	return ok && oauthProv.ChannelAllowed(channel)
}

func oauthTokenURLMatchesProvider(provider, tokenURL string) bool {
	oauthProv, ok := oauthprovider.Get(provider)
	return ok && oauthProv.TokenURLAllowed(tokenURL)
}

func sanitizeOAuthError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "api_key"} {
		msg = redactOAuthSecretField(msg, key)
	}
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}

func redactOAuthSecretField(msg, key string) string {
	separators := []byte{':', '=', '&'}
	for {
		lower := strings.ToLower(msg)
		idx := strings.Index(lower, strings.ToLower(key))
		if idx < 0 {
			return msg
		}
		sepIdx := -1
		for i := idx + len(key); i < len(msg); i++ {
			if msg[i] == ' ' || msg[i] == '\t' || msg[i] == '"' || msg[i] == '\'' {
				continue
			}
			for _, sep := range separators {
				if msg[i] == sep {
					sepIdx = i
				}
			}
			break
		}
		if sepIdx < 0 {
			return msg
		}
		start := sepIdx + 1
		for start < len(msg) && (msg[start] == ' ' || msg[start] == '\t' || msg[start] == '"' || msg[start] == '\'') {
			start++
		}
		end := start
		for end < len(msg) && msg[end] != ',' && msg[end] != '}' && msg[end] != '"' && msg[end] != '\'' && msg[end] != '&' {
			end++
		}
		if end <= start {
			return msg
		}
		if strings.HasPrefix(msg[start:], "[redacted]") {
			return msg
		}
		msg = msg[:start] + "[redacted]" + msg[end:]
	}
}

type oauthJSONImport struct {
	State        string
	Code         string
	CallbackURL  string
	Error        string
	RedirectURI  string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	TokenURL     string
	AccessToken  string
	IDToken      string
	APIKey       string
	RefreshToken string
	Scope        string
	TokenType    string
	Expiry       *time.Time
}

func parseOAuthJSON(raw string) (*oauthJSONImport, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &data); err != nil {
		return nil, fmt.Errorf("invalid oauth_json")
	}
	imp := &oauthJSONImport{
		State:        jsonString(data, "state", "oauth_state"),
		Code:         jsonString(data, "code", "authorization_code", "auth_code"),
		CallbackURL:  jsonString(data, "callback_url", "redirect_url", "url"),
		Error:        jsonString(data, "error", "error_description"),
		RedirectURI:  jsonString(data, "redirect_uri"),
		CodeVerifier: jsonString(data, "code_verifier", "verifier"),
		ClientID:     jsonString(data, "client_id"),
		ClientSecret: jsonString(data, "client_secret"),
		TokenURL:     jsonString(data, "token_url"),
		AccessToken:  jsonString(data, "access_token", "token"),
		IDToken:      jsonString(data, "id_token"),
		APIKey:       jsonString(data, "api_key", "key"),
		RefreshToken: jsonString(data, "refresh_token"),
		Scope:        jsonString(data, "scope"),
		TokenType:    jsonString(data, "token_type"),
		Expiry:       jsonExpiry(data),
	}
	if imp.CallbackURL != "" {
		if parsed, err := url.Parse(imp.CallbackURL); err == nil {
			values := parsed.Query()
			if imp.Code == "" {
				imp.Code = values.Get("code")
			}
			if imp.State == "" {
				imp.State = values.Get("state")
			}
			if imp.Error == "" {
				imp.Error = values.Get("error")
			}
		}
	}
	return imp, nil
}

func (h *Handler) applyOAuthJSONImport(state string, imp *oauthJSONImport) {
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	current := h.oauthSessions[state]
	if current == nil {
		return
	}
	if imp.RedirectURI != "" {
		current.RedirectURI = imp.RedirectURI
	}
	if imp.CodeVerifier != "" {
		current.CodeVerifier = imp.CodeVerifier
	}
	if imp.ClientID != "" {
		current.ClientID = imp.ClientID
	}
	if imp.ClientSecret != "" {
		current.ClientSecret = imp.ClientSecret
	}
	if imp.TokenURL != "" {
		if oauthTokenURLMatchesProvider(current.Provider, imp.TokenURL) {
			current.TokenURL = imp.TokenURL
		}
	}
}

func jsonString(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := data[key]; ok {
			switch value := v.(type) {
			case string:
				if strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			case float64:
				return strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
	return ""
}

func jsonExpiry(data map[string]interface{}) *time.Time {
	for _, key := range []string{"token_expiry", "expires_at", "expiry", "expiry_date", "expires_in"} {
		v, ok := data[key]
		if !ok {
			continue
		}
		switch value := v.(type) {
		case string:
			if value == "" {
				continue
			}
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				return expiryFromNumber(key, n)
			}
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				return &t
			}
		case float64:
			return expiryFromNumber(key, int64(value))
		}
	}
	return nil
}

func expiryFromNumber(key string, n int64) *time.Time {
	var t time.Time
	if key == "expires_in" {
		t = time.Now().Add(time.Duration(n) * time.Second)
	} else if n > 10_000_000_000 {
		t = time.UnixMilli(n)
	} else {
		t = time.Unix(n, 0)
	}
	return &t
}

func (h *Handler) OAuthCallback(ctx *fasthttp.RequestCtx) {
	state := string(ctx.QueryArgs().Peek("state"))
	code := string(ctx.QueryArgs().Peek("code"))
	providerErr := string(ctx.QueryArgs().Peek("error"))
	if state == "" {
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "OAuth state is missing")
		return
	}

	session := h.getOAuthSession(state)
	if session == nil {
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "OAuth session was not found or expired")
		return
	}
	// Verify the callback request originates from the same IP that started the OAuth flow
	if session.CreatedByIP != "" && session.CreatedByIP != ctx.RemoteIP().String() {
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusForbidden, "OAuth callback IP mismatch - possible session hijacking attempt")
		return
	}
	if providerErr != "" {
		h.completeOAuthSession(state, "", "", nil, nil, providerErr)
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "OAuth provider returned an error")
		return
	}
	if code == "" {
		h.completeOAuthSession(state, "", "", nil, nil, "authorization code is missing")
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "Authorization code is missing")
		return
	}

	credential, refreshToken, expiry, metadata, err := exchangeOAuthCode(session, code)
	if err != nil {
		h.completeOAuthSession(state, "", "", nil, nil, sanitizeOAuthError(err))
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadGateway, "OAuth token exchange failed")
		return
	}
	h.completeOAuthSession(state, credential, refreshToken, expiry, metadata, "")
	h.writeOAuthCallbackPage(ctx, fasthttp.StatusOK, "OAuth authorization completed. You can return to UAPI and bind the account.")
}

func (h *Handler) OAuthStatus(ctx *fasthttp.RequestCtx) {
	state := string(ctx.QueryArgs().Peek("state"))
	if state == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "state is required")
		return
	}
	session := h.getOAuthSession(state)
	if session == nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "oauth session not found")
		return
	}
	h.jsonResponse(ctx, 200, h.oauthStatusDTO(session))
}

func (h *Handler) BindOAuthAccount(ctx *fasthttp.RequestCtx) {
	adminUsername, _ := ctx.UserValue("admin_user").(string)
	var req BindOAuthAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.State == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "state is required")
		return
	}
	session := h.getOAuthSession(req.State)
	if session == nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "oauth session not found")
		return
	}
	// Verify the admin who started the OAuth flow is the same one binding it
	if session.CreatedBy != "" && session.CreatedBy != adminUsername {
		h.jsonError(ctx, fasthttp.StatusForbidden, "oauth session was started by a different admin")
		return
	}
	if session.Status != "completed" {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session is not completed")
		return
	}
	if session.Credential == "" {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session has no credential")
		return
	}
	if session.RefreshToken == "" {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session has no refresh token")
		return
	}
	if session.BoundAccountID != nil {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session is already bound")
		return
	}
	if !oauthTokenURLMatchesProvider(session.Provider, session.TokenURL) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth token_url does not match provider")
		return
	}
	var channel db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", session.ChannelID).First(&channel).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	if !channelAllowsOAuthProvider(channel, session.Provider) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth account can only be bound to the matching OAuth channel format")
		return
	}

	encryptedCredential, err := internalcrypto.Encrypt(session.Credential)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt credential failed")
		return
	}
	encryptedRefresh, err := internalcrypto.Encrypt(session.RefreshToken)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt refresh token failed")
		return
	}
	encryptedClientSecret := ""
	if session.ClientSecret != "" {
		encryptedClientSecret, err = internalcrypto.Encrypt(session.ClientSecret)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt client secret failed")
			return
		}
	}

	name := strings.TrimSpace(req.AccountName)
	if name == "" {
		name = session.AccountName
	}
	if name == "" {
		name = session.Provider + "-oauth"
	}
	weight := req.Weight
	if weight <= 0 {
		weight = 1
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	account := db.Account{
		ChannelID: session.ChannelID, Name: name, Credentials: encryptedCredential,
		CredType: "oauth_token", Endpoint: upstreamconfig.DefaultEndpoint(channel.Type, channel.APIFormat), Weight: weight, Enabled: enabled,
		RefreshToken: encryptedRefresh, TokenExpiry: session.Expiry,
		ClientID: session.ClientID, ClientSecret: encryptedClientSecret,
		TokenURL: session.TokenURL,
		Metadata: session.Metadata,
	}
	if oauthProv, ok := oauthprovider.Get(session.Provider); ok {
		if metadata, err := oauthProv.SyncMetadata(session.Credential, account.Metadata); err == nil {
			account.Metadata = mergeMetadata(account.Metadata, metadata)
		} else {
			logger.Warnf("admin.oauth", "oauth metadata sync failed", logger.F("provider", session.Provider), logger.F("channel_id", account.ChannelID.String()), logger.Err(err))
		}
	}
	account.ID = uuid.New()
	if err := h.db.Create(&account).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create account failed")
		return
	}

	h.oauthMu.Lock()
	if current := h.oauthSessions[req.State]; current != nil {
		current.Status = "bound"
		current.BoundAccountID = &account.ID
	}
	h.oauthMu.Unlock()

	if h.RefreshPool != nil {
		h.RefreshPool(account.ChannelID.String())
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&account)
	}
	auditCreateCtx(h.db, "account", account.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": account.Name, "channel_id": account.ChannelID, "cred_type": account.CredType, "oauth_provider": session.Provider})
	logger.Infof("admin.oauth", "oauth account bound", logger.F("provider", session.Provider), logger.F("channel_id", account.ChannelID.String()), logger.F("account_id", account.ID.String()), logger.F("enabled", account.Enabled))
	h.jsonResponse(ctx, 200, account)
}

func (h *Handler) oauthRedirectURI(ctx *fasthttp.RequestCtx) string {
	proto := string(ctx.Request.Header.Peek("X-Forwarded-Proto"))
	if proto == "" {
		if ctx.IsTLS() {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := string(ctx.Request.Header.Peek("X-Forwarded-Host"))
	if host == "" {
		host = string(ctx.Host())
	}
	return proto + "://" + host + "/api/admin/channels/oauth/callback"
}

func (h *Handler) getOAuthSession(state string) *oauthSession {
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.pruneOAuthSessionsLocked()
	session := h.oauthSessions[state]
	if session == nil {
		return nil
	}
	copy := *session
	return &copy
}

func (h *Handler) completeOAuthSession(state, credential, refreshToken string, expiry *time.Time, metadata map[string]interface{}, errMsg string) {
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	session := h.oauthSessions[state]
	if session == nil {
		return
	}
	now := time.Now()
	session.CompletedAt = &now
	if errMsg != "" {
		session.Status = "error"
		session.Error = errMsg
		return
	}
	session.Status = "completed"
	session.Credential = credential
	session.RefreshToken = refreshToken
	session.Expiry = expiry
	session.Metadata = metadata
	session.Error = ""
}

func (h *Handler) pollOpenAIDeviceOAuth(state, deviceAuthID, userCode string, interval int) {
	if interval <= 0 {
		interval = 5
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(oauthSessionTTL)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			h.completeOAuthSession(state, "", "", nil, nil, "device authorization expired")
			return
		case <-ticker.C:
			session := h.getOAuthSession(state)
			if session == nil || session.Status != "pending" {
				return
			}
			deviceToken, ready, err := openai.PollDeviceToken(deviceAuthID, userCode)
			if err != nil {
				h.completeOAuthSession(state, "", "", nil, nil, sanitizeOAuthError(err))
				return
			}
			if !ready {
				continue
			}
			session.CodeVerifier = deviceToken.CodeVerifier
			credential, refreshToken, expiry, metadata, err := exchangeOAuthCode(session, deviceToken.AuthorizationCode)
			if err != nil {
				h.completeOAuthSession(state, "", "", nil, nil, sanitizeOAuthError(err))
				return
			}
			h.completeOAuthSession(state, credential, refreshToken, expiry, metadata, "")
			return
		}
	}
}

func (h *Handler) pruneOAuthSessionsLocked() {
	cutoff := time.Now().Add(-oauthSessionTTL)
	for state, session := range h.oauthSessions {
		if session.CreatedAt.Before(cutoff) || session.Status == "bound" {
			delete(h.oauthSessions, state)
		}
	}
}

func (h *Handler) oauthStatusDTO(session *oauthSession) OAuthStatusResponse {
	resp := OAuthStatusResponse{
		State: session.State, Provider: session.Provider, ChannelID: session.ChannelID,
		Status: session.Status, Error: session.Error, CreatedAt: session.CreatedAt,
		CompletedAt: session.CompletedAt, BoundAccountID: session.BoundAccountID,
	}
	if session.Credential != "" {
		resp.ReadyToBind = true
	}
	return resp
}

func (h *Handler) writeOAuthCallbackPage(ctx *fasthttp.RequestCtx, status int, message string) {
	ctx.SetStatusCode(status)
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetBodyString("<!doctype html><meta charset=\"utf-8\"><title>UAPI OAuth</title><body><main style=\"font-family:system-ui,sans-serif;max-width:640px;margin:64px auto;line-height:1.5\"><h1>UAPI OAuth</h1><p>" + html.EscapeString(message) + "</p></main></body>")
}

func oauthPKCE(provider string) (verifier, challenge string, err error) {
	oauthProv, ok := oauthprovider.Get(provider)
	if !ok {
		return "", "", fmt.Errorf("unsupported provider")
	}
	return oauthProv.PKCE()
}

func buildProviderAuthURL(provider, clientID, redirectURI, challenge, state string) string {
	oauthProv, ok := oauthprovider.Get(provider)
	if !ok {
		return ""
	}
	return oauthProv.BuildAuthURL(clientID, redirectURI, challenge, state)
}

func exchangeOAuthCode(session *oauthSession, code string) (credential, refreshToken string, expiry *time.Time, metadata map[string]interface{}, err error) {
	oauthProv, ok := oauthprovider.Get(session.Provider)
	if !ok {
		return "", "", nil, nil, fmt.Errorf("unsupported provider")
	}
	result, err := oauthProv.Exchange(oauthprovider.ExchangeRequest{
		Code: code, RedirectURI: session.RedirectURI, CodeVerifier: session.CodeVerifier,
		ClientID: session.ClientID, ClientSecret: session.ClientSecret, TokenURL: session.TokenURL, State: session.State,
	})
	if err != nil {
		return "", "", nil, nil, err
	}
	metadata = result.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["oauth_provider"] = session.Provider
	return result.Credential, result.RefreshToken, result.Expiry, metadata, nil
}

func geminiProjectID(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	if project, ok := metadata["project_id"].(string); ok {
		return project
	}
	if loadRes, ok := metadata["load_code_assist"].(map[string]interface{}); ok {
		if project, ok := loadRes["cloudaicompanionProject"].(string); ok {
			return project
		}
		if project, ok := loadRes["cloudaicompanionProject"].(map[string]interface{}); ok {
			if id, ok := project["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}

func openAIAccountID(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	if accountID, ok := metadata["chatgpt_account_id"].(string); ok {
		return accountID
	}
	return ""
}

func openAIFedRAMP(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	if fedramp, ok := metadata["chatgpt_account_is_fedramp"].(bool); ok {
		return fedramp
	}
	return false
}

func importedMetadata(imp *oauthJSONImport) map[string]interface{} {
	metadata := map[string]interface{}{
		"oauth_imported_at": time.Now().UTC().Format(time.RFC3339),
	}
	if imp == nil {
		return metadata
	}
	if imp.Scope != "" {
		metadata["oauth_scope"] = imp.Scope
	}
	if imp.TokenType != "" {
		metadata["oauth_token_type"] = imp.TokenType
	}
	if imp.IDToken != "" {
		metadata["oauth_has_id_token"] = true
	}
	if imp.Expiry != nil {
		metadata["oauth_expiry"] = imp.Expiry.UTC().Format(time.RFC3339)
	}
	return metadata
}

func mergeMetadata(base, overlay map[string]interface{}) map[string]interface{} {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := map[string]interface{}{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func geminiLoopbackRedirectURI() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return gemini.DefaultRedirectURI
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return gemini.DefaultRedirectURI
	}
	return fmt.Sprintf("http://127.0.0.1:%d/oauth2callback", addr.Port)
}
