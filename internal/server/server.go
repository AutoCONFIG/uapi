package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/manager"
	"github.com/AutoCONFIG/cli-relay/internal/provider"
	"golang.org/x/crypto/bcrypt"
)

// Server is the HTTP API server.
type Server struct {
	manager   *manager.TokenManager
	db        *db.DB
	logger    *slog.Logger
	mux       *http.ServeMux
	sessions  map[string]time.Time
	oauth     *OAuthProxy
	mu        sync.Mutex
}

// New creates a new HTTP server.
func New(m *manager.TokenManager, database *db.DB, logger *slog.Logger) *Server {
	s := &Server{
		manager:  m,
		db:       database,
		logger:   logger,
		mux:      http.NewServeMux(),
		sessions: make(map[string]time.Time),
		oauth:    NewOAuthProxy(m, logger),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Public API routes
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleAdminLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.handleAdminLogout)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)

	// Protected API routes
	s.mux.HandleFunc("GET /api/v1/providers", s.requireAuth(s.handleListProviders))
	s.mux.HandleFunc("GET /api/v1/providers/{name}/token", s.requireAuth(s.handleGetToken))
	s.mux.HandleFunc("POST /api/v1/providers/{name}/login", s.requireAuth(s.handleLogin))
	s.mux.HandleFunc("DELETE /api/v1/providers/{name}/token", s.requireAuth(s.handleLogout))
	s.mux.HandleFunc("POST /api/v1/providers/{name}/refresh", s.requireAuth(s.handleRefresh))
	s.mux.HandleFunc("POST /api/v1/providers/{name}/recover", s.requireAuth(s.handleRecover))

	// OAuth proxy routes
	s.oauth.RegisterRoutes(s.mux)

	// Web UI
	s.mux.Handle("/", http.FileServer(http.FS(webUIFS)))
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// --- Auth middleware ---

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hasAdmin, err := s.db.HasAdmin()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// If no admin set, allow all access (first-time setup)
		if !hasAdmin {
			next(w, r)
			return
		}

		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else {
			if c, err := r.Cookie("session"); err == nil {
				token = c.Value
			}
		}

		if token == "" || !s.validSession(token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next(w, r)
	}
}

func (s *Server) validSession(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiry, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.sessions, token)
		return false
	}
	return true
}

func (s *Server) createSession() string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()

	return token
}

func (s *Server) removeSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeError(w, http.StatusBadRequest, "password required")
		return
	}

	hash, err := s.db.GetAdminPassword()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if hash == "" {
		// First login: set the password
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if err := s.db.SetAdminPassword(string(h)); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		hash = string(h)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	token := s.createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "token": token})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil {
		s.removeSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	hasAdmin, _ := s.db.HasAdmin()
	writeJSON(w, http.StatusOK, map[string]bool{"has_admin": hasAdmin})
}

func (s *Server) handleListProviders(w http.ResponseWriter, _ *http.Request) {
	statuses := s.manager.Status(context.Background())
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": statuses})
}

func (s *Server) handleGetToken(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return
	}

	tokens, err := s.manager.GetToken(r.Context(), name)
	if err != nil {
		status := http.StatusNotFound
		if err.Error() != "" {
			status = http.StatusUnauthorized
		}
		writeError(w, status, err.Error())
		return
	}

	resp := tokenResponse{
		AccessToken:  tokens.AccessToken,
		TokenType:    "Bearer",
		AccountID:    tokens.AccountID,
		ExpiresAt:    tokens.ExpiresAt,
		ExtraHeaders: tokens.ExtraHeaders,
	}
	if tokens.APIKey != "" {
		resp.AccessToken = tokens.APIKey
		resp.TokenType = "ApiKey"
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return
	}

	var req struct {
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Method = "browser"
	}

	method := provider.AuthMethod(req.Method)
	if method == "" {
		method = provider.AuthMethodBrowser
	}

	tokens, err := s.manager.Login(r.Context(), name, method)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "success",
		"provider":    name,
		"auth_method": method,
		"expires_at":  tokens.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return
	}

	if err := s.manager.Logout(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return
	}

	tokens, err := s.manager.RefreshForce(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "refreshed",
		"expires_at": tokens.ExpiresAt,
	})
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return
	}

	if err := s.manager.Recover(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "recovered"})
}

type tokenResponse struct {
	AccessToken  string            `json:"access_token"`
	TokenType    string            `json:"token_type"`
	AccountID    string            `json:"account_id,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
