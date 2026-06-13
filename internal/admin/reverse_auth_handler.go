package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const reverseAuthProvider = "chatgpt_reverse"
const reverseAuthBase = "https://auth.openai.com"
const reversePlatformBase = "https://platform.openai.com"
const reversePlatformClientID = "app_2SKx67EdpoN0G6j64rFvigXD"
const reversePlatformAudience = "https://api.openai.com/v1"
const reversePlatformRedirectURI = "https://platform.openai.com/auth/callback"
const reversePlatformAuth0Client = "eyJuYW1lIjoiYXV0aDAtc3BhLWpzIiwidmVyc2lvbiI6IjEuMjEuMCJ9"
const reverseRefreshTokenURL = "https://auth.openai.com/oauth/token"
const reverseExchangeTokenURL = "https://auth.openai.com/api/accounts/oauth/token"

var reverseAuthHTTPClient = &http.Client{Timeout: 60 * time.Second}

func (h *Handler) StartReverseAuth(ctx *fasthttp.RequestCtx) {
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
	if req.ChannelID == uuid.Nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel_id is required")
		return
	}
	var channel db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", req.ChannelID).First(&channel).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	if !isReverseAPIFormat(channel.APIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "reverse auth requires a reverse channel")
		return
	}
	state, err := randomURLToken(32)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create state failed")
		return
	}
	verifier, err := openai.GenerateCodeVerifier()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create pkce failed")
		return
	}
	challenge := openai.GenerateCodeChallenge(verifier)
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = reversePlatformClientID
	}
	tokenURL := strings.TrimSpace(req.TokenURL)
	if tokenURL == "" {
		tokenURL = reverseRefreshTokenURL
	}
	if !isOpenAIReverseTokenURL(tokenURL) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "reverse token_url must be an OpenAI auth token endpoint")
		return
	}
	redirectURI := reversePlatformRedirectURI
	authURL := buildReverseAuthURL(clientID, redirectURI, challenge, state)
	session := &oauthSession{
		State: state, Provider: reverseAuthProvider, ChannelID: req.ChannelID,
		AccountName: strings.TrimSpace(req.AccountName),
		ClientID:    clientID, TokenURL: tokenURL, RedirectURI: redirectURI, CodeVerifier: verifier,
		Status: "pending", CreatedAt: time.Now(),
		CreatedBy: adminUsername, CreatedByIP: ctx.RemoteIP().String(),
	}
	h.oauthMu.Lock()
	h.pruneOAuthSessionsLocked()
	h.oauthSessions[state] = session
	h.oauthMu.Unlock()
	logger.Infof("admin.reverse_auth", "reverse auth session started", logger.F("channel_id", req.ChannelID.String()))
	h.jsonResponse(ctx, 200, OAuthAuthURLResponse{
		AuthURL: authURL, State: state, RedirectURI: redirectURI, Mode: "manual_callback", ExpiresAt: session.CreatedAt.Add(oauthSessionTTL),
	})
}

func (h *Handler) CompleteReverseAuth(ctx *fasthttp.RequestCtx) {
	var req CompleteOAuthRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	state := strings.TrimSpace(req.State)
	code := strings.TrimSpace(req.Code)
	providerErr := ""
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
		h.jsonError(ctx, fasthttp.StatusNotFound, "reverse auth session not found")
		return
	}
	if session.Provider != reverseAuthProvider {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "session is not a reverse auth session")
		return
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
	tokens, err := exchangeReverseAuthCode(code, session.RedirectURI, session.CodeVerifier, session.ClientID)
	if err != nil {
		safeErr := sanitizeOAuthError(err)
		h.completeOAuthSession(state, "", "", nil, nil, safeErr)
		h.jsonError(ctx, fasthttp.StatusBadGateway, safeErr)
		return
	}
	expiry := time.Now().Add(8 * 24 * time.Hour)
	if tokens.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	metadata := map[string]interface{}{"auth_provider": reverseAuthProvider}
	if tokens.IDToken != "" {
		if parsed, err := openai.ParseIDTokenMetadata(tokens.IDToken); err == nil {
			metadata = mergeMetadata(metadata, parsed)
		}
	}
	h.completeOAuthSession(state, tokens.AccessToken, tokens.RefreshToken, &expiry, metadata, "")
	h.jsonResponse(ctx, 200, h.oauthStatusDTO(h.getOAuthSession(state)))
}

func isOpenAIReverseTokenURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "https") &&
		strings.EqualFold(parsed.Hostname(), "auth.openai.com") &&
		parsed.EscapedPath() == "/oauth/token"
}

func buildReverseAuthURL(clientID, redirectURI, challenge, state string) string {
	params := url.Values{}
	params.Set("issuer", reverseAuthBase)
	params.Set("client_id", clientID)
	params.Set("audience", reversePlatformAudience)
	params.Set("redirect_uri", redirectURI)
	params.Set("device_id", uuid.NewString())
	params.Set("screen_hint", "login_or_signup")
	params.Set("max_age", "0")
	params.Set("scope", "openid profile email offline_access")
	params.Set("response_type", "code")
	params.Set("response_mode", "query")
	params.Set("state", state)
	params.Set("nonce", uuid.NewString())
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("auth0Client", reversePlatformAuth0Client)
	return reverseAuthBase + "/api/accounts/authorize?" + params.Encode()
}

type reverseTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeReverseAuthCode(code, redirectURI, verifier, clientID string) (*reverseTokenResponse, error) {
	payload, _ := json.Marshal(map[string]string{
		"client_id":     clientID,
		"code_verifier": verifier,
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
	})
	req, err := http.NewRequest(http.MethodPost, reverseExchangeTokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", reversePlatformBase)
	req.Header.Set("Referer", reversePlatformBase+"/")
	req.Header.Set("auth0-client", reversePlatformAuth0Client)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0")
	debugInfo := oauthdebug.NewHTTPDebug(req, payload)
	resp, err := reverseAuthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	var out reverseTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, nil, err)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || out.AccessToken == "" {
		detail := strings.TrimSpace(firstNonEmpty(out.ErrorDescription, out.Error, string(body)))
		err := fmt.Errorf("reverse auth token exchange failed: status %d: %s", resp.StatusCode, detail)
		oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, nil, err)
		return nil, err
	}
	if out.RefreshToken == "" {
		err := fmt.Errorf("reverse auth token exchange missing refresh_token")
		oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write(reverseAuthProvider, "exchange_code", reverseOAuthDebugMetadata(clientID, redirectURI), debugInfo, out, nil)
	return &out, nil
}

func reverseOAuthDebugMetadata(clientID, redirectURI string) map[string]interface{} {
	return map[string]interface{}{
		"client_id":    clientID,
		"redirect_uri": redirectURI,
		"token_url":    reverseExchangeTokenURL,
	}
}

func reverseAuthAccountType(session *oauthSession) string {
	if session != nil && session.Provider == reverseAuthProvider {
		return "chatgpt_reverse"
	}
	return "oauth_token"
}
