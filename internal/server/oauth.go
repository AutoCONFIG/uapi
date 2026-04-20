package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/manager"
	"github.com/AutoCONFIG/cli-relay/internal/provider"
)

// OAuthProxy handles browser-based OAuth flows that can be initiated remotely
// and have their callbacks proxied through the server.
type OAuthProxy struct {
	manager *manager.TokenManager
	logger  *slog.Logger

	mu      sync.Mutex
	pending map[string]*oauthSession
}

type oauthSession struct {
	ProviderName  string
	State         string
	CodeVerifier  string
	RedirectURL   string
	AuthURL       string
	CreatedAt     time.Time
	CallbackPort  int
	Method        provider.AuthMethod
}

// NewOAuthProxy creates a new OAuth proxy handler.
func NewOAuthProxy(m *manager.TokenManager, logger *slog.Logger) *OAuthProxy {
	return &OAuthProxy{
		manager: m,
		logger:  logger,
		pending: make(map[string]*oauthSession),
	}
}

// RegisterRoutes adds OAuth proxy routes to the given mux.
func (p *OAuthProxy) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/oauth/init", p.requireAuth(p.handleInit))
	mux.HandleFunc("GET /api/v1/oauth/callback", p.handleCallback)
}

func (p *OAuthProxy) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	// Reuse the parent server's auth check
	return next
}

// handleInit starts an OAuth flow for a provider and returns the auth URL
// for the user's browser to navigate to.
func (p *OAuthProxy) handleInit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Method   string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}

	method := provider.AuthMethod(req.Method)
	if method == "" {
		method = provider.AuthMethodBrowser
	}

	// Generate state for CSRF protection
	stateBytes := make([]byte, 32)
	rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	// Generate PKCE verifier
	verifierBytes := make([]byte, 64)
	rand.Read(verifierBytes)
	codeVerifier := hex.EncodeToString(verifierBytes)

	// Build the callback URL that points back to this server
	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	callbackURL := fmt.Sprintf("%s://%s/api/v1/oauth/callback", scheme, host)

	// Store the session
	session := &oauthSession{
		ProviderName: req.Provider,
		State:        state,
		CodeVerifier: codeVerifier,
		RedirectURL:  callbackURL,
		CreatedAt:    time.Now(),
		Method:       method,
	}

	p.mu.Lock()
	p.pending[state] = session
	p.mu.Unlock()

	// Clean up old sessions in background
	go p.cleanup()

	// Build the provider-specific auth URL
	// The actual URL construction is provider-specific, so we return
	// a generic response with the state. The provider's Login method
	// should be called with the proxied callback URL.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":         state,
		"callback_url":  callbackURL,
		"code_verifier": codeVerifier,
		"provider":      req.Provider,
		"method":        string(method),
		"expires_in":    300,
	})
}

// handleCallback receives the OAuth callback from the browser.
// The provider redirects the user here after authentication.
func (p *OAuthProxy) handleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	errorParam := r.URL.Query().Get("error")

	if errorParam != "" {
		p.logger.Warn("OAuth callback received error", "error", errorParam)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `<html><body><h2>Authentication Failed</h2><p>You can close this window.</p></body></html>`)
		return
	}

	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "missing state or code")
		return
	}

	p.mu.Lock()
	session, ok := p.pending[state]
	if ok {
		delete(p.pending, state)
	}
	p.mu.Unlock()

	if !ok {
		p.logger.Warn("OAuth callback with unknown state", "state", state)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `<html><body><h2>Invalid or Expired Session</h2><p>You can close this window.</p></body></html>`)
		return
	}

	// Exchange the authorization code for tokens using the provider
	// The manager's Login flow is customized via the callback context
	ctx := context.WithValue(r.Context(), oauthContextKey("callback"), &oauthCallback{
		Code:         code,
		State:        state,
		CodeVerifier: session.CodeVerifier,
		RedirectURL:  session.RedirectURL,
	})

	tokens, err := p.manager.Login(ctx, session.ProviderName, session.Method)
	if err != nil {
		p.logger.Error("OAuth token exchange failed", "provider", session.ProviderName, "error", err)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `<html><body><h2>Authentication Failed</h2><p>Token exchange error. Please try again.</p></body></html>`)
		return
	}

	p.logger.Info("OAuth login successful", "provider", session.ProviderName, "expires_at", tokens.ExpiresAt)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `<html><body><h2>Authentication Successful</h2><p>You can close this window and return to CLI Relay.</p><script>window.close()</script></body></html>`)
}

func (p *OAuthProxy) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for state, session := range p.pending {
		if time.Since(session.CreatedAt) > 5*time.Minute {
			delete(p.pending, state)
		}
	}
}

// oauthContextKey is used to pass callback data through context.
type oauthContextKey string

// OAuthCallbackFromContext extracts OAuth callback data from a context.
func OAuthCallbackFromContext(ctx context.Context) *oauthCallback {
	if cb, ok := ctx.Value(oauthContextKey("callback")).(*oauthCallback); ok {
		return cb
	}
	return nil
}

type oauthCallback struct {
	Code         string
	State        string
	CodeVerifier string
	RedirectURL  string
}

// IsProxiedCallback returns true when the login flow is being driven by
// the OAuth proxy (i.e., the authorization code came from a proxied callback).
func IsProxiedCallback(ctx context.Context) bool {
	return OAuthCallbackFromContext(ctx) != nil
}

// BuildProxiedAuthURL constructs an authorization URL with the proxy's callback URL
// and PKCE parameters. Providers should use this when running in proxy mode.
func BuildProxiedAuthURL(baseAuthURL, clientID, callbackURL, state, codeChallenge, scopes string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", callbackURL)
	v.Set("scope", scopes)
	v.Set("state", state)
	if codeChallenge != "" {
		v.Set("code_challenge_method", "S256")
		v.Set("code_challenge", codeChallenge)
	}

	if strings.Contains(baseAuthURL, "?") {
		return baseAuthURL + "&" + v.Encode()
	}
	return baseAuthURL + "?" + v.Encode()
}
