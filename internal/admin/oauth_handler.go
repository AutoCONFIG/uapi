package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	internalcrypto "github.com/AutoCONFIG/cli-relay/internal/crypto"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider/openai"
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
	CreatedAt      time.Time
	CompletedAt    *time.Time
	BoundAccountID *uuid.UUID
}

func (h *Handler) StartOAuth(ctx *fasthttp.RequestCtx) {
	var req StartOAuthRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
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
		provider = strings.ToLower(channel.Type)
	}
	if provider != "openai" && provider != "gemini" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "oauth is supported for openai and gemini channels")
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
	tokenURL := strings.TrimSpace(req.TokenURL)
	if clientID == "" {
		clientID = defaultOAuthClientID(provider)
	}
	if tokenURL == "" {
		tokenURL = defaultOAuthTokenURL(provider)
	}
	redirectURI := h.oauthRedirectURI(ctx)
	authURL := buildProviderAuthURL(provider, clientID, redirectURI, challenge, state)

	session := &oauthSession{
		State: state, Provider: provider, ChannelID: req.ChannelID,
		AccountName: strings.TrimSpace(req.AccountName),
		ClientID:    clientID, ClientSecret: strings.TrimSpace(req.ClientSecret),
		TokenURL: tokenURL, RedirectURI: redirectURI, CodeVerifier: verifier,
		Status: "pending", CreatedAt: time.Now(),
	}
	h.oauthMu.Lock()
	h.pruneOAuthSessionsLocked()
	h.oauthSessions[state] = session
	h.oauthMu.Unlock()

	h.jsonResponse(ctx, 200, OAuthAuthURLResponse{
		AuthURL:     authURL,
		State:       state,
		RedirectURI: redirectURI,
		ExpiresAt:   session.CreatedAt.Add(oauthSessionTTL),
	})
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
	if providerErr != "" {
		h.completeOAuthSession(state, "", "", nil, providerErr)
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "OAuth provider returned an error")
		return
	}
	if code == "" {
		h.completeOAuthSession(state, "", "", nil, "authorization code is missing")
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadRequest, "Authorization code is missing")
		return
	}

	credential, refreshToken, expiry, err := exchangeOAuthCode(session, code)
	if err != nil {
		h.completeOAuthSession(state, "", "", nil, err.Error())
		h.writeOAuthCallbackPage(ctx, fasthttp.StatusBadGateway, "OAuth token exchange failed")
		return
	}
	h.completeOAuthSession(state, credential, refreshToken, expiry, "")
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
	if session.Status != "completed" {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session is not completed")
		return
	}
	if session.BoundAccountID != nil {
		h.jsonError(ctx, fasthttp.StatusConflict, "oauth session is already bound")
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
		CredType: "oauth_token", Weight: weight, Enabled: enabled,
		RefreshToken: encryptedRefresh, TokenExpiry: session.Expiry,
		ClientID: session.ClientID, ClientSecret: encryptedClientSecret,
		TokenURL: session.TokenURL,
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
	auditCreate(h.db, "account", account.ID, h.getAdminUser(ctx))
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

func (h *Handler) completeOAuthSession(state, credential, refreshToken string, expiry *time.Time, errMsg string) {
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
	session.Error = ""
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
	ctx.SetBodyString("<!doctype html><meta charset=\"utf-8\"><title>UAPI OAuth</title><body><main style=\"font-family:system-ui,sans-serif;max-width:640px;margin:64px auto;line-height:1.5\"><h1>UAPI OAuth</h1><p>" + message + "</p></main></body>")
}

func oauthPKCE(provider string) (verifier, challenge string, err error) {
	switch provider {
	case "openai":
		verifier, err = openai.GenerateCodeVerifier()
		if err != nil {
			return "", "", err
		}
		return verifier, openai.GenerateCodeChallenge(verifier), nil
	case "gemini":
		verifier, err = gemini.GenerateCodeVerifier()
		if err != nil {
			return "", "", err
		}
		return verifier, gemini.GenerateCodeChallenge(verifier), nil
	default:
		return "", "", fmt.Errorf("unsupported provider")
	}
}

func buildProviderAuthURL(provider, clientID, redirectURI, challenge, state string) string {
	if provider == "gemini" {
		return gemini.BuildAuthURL(clientID, redirectURI, challenge, state)
	}
	return openai.BuildAuthURL(clientID, redirectURI, challenge, state)
}

func defaultOAuthClientID(provider string) string {
	if provider == "gemini" {
		return gemini.DefaultClientID
	}
	return openai.DefaultClientID
}

func defaultOAuthTokenURL(provider string) string {
	if provider == "gemini" {
		return gemini.DefaultTokenURL
	}
	return openai.DefaultTokenURL
}

func exchangeOAuthCode(session *oauthSession, code string) (credential, refreshToken string, expiry *time.Time, err error) {
	switch session.Provider {
	case "openai":
		tokens, err := openai.ExchangeCode(session.TokenURL, code, session.RedirectURI, session.CodeVerifier, session.ClientID)
		if err != nil {
			return "", "", nil, err
		}
		credential = tokens.AccessToken
		if tokens.IDToken != "" {
			if exchanged, err := openai.ExchangeForAPIKey(session.TokenURL, tokens.IDToken, session.ClientID); err == nil && exchanged != "" {
				credential = exchanged
			}
		}
		exp := time.Now().Add(8 * 24 * time.Hour)
		if tokens.ExpiresIn > 0 {
			exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		}
		return credential, tokens.RefreshToken, &exp, nil
	case "gemini":
		tokens, err := gemini.ExchangeCode(session.TokenURL, code, session.RedirectURI, session.CodeVerifier, session.ClientID, session.ClientSecret)
		if err != nil {
			return "", "", nil, err
		}
		exp := time.Now().Add(time.Hour)
		if tokens.ExpiresIn > 0 {
			exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		}
		return tokens.AccessToken, tokens.RefreshToken, &exp, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported provider")
	}
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
