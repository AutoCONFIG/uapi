package server

import (
	"crypto/subtle"
	"fmt"
	"io/fs"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/admin"
	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/gateway"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/quota"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/AutoCONFIG/uapi/internal/user"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type Server struct {
	cfg            *config.Config
	cfgPath        string
	db             *gorm.DB
	pools          *relay.PoolManager
	billing        *relay.BillingService
	gateway        *gateway.Gateway
	affinity       *relay.AffinityCache
	adminHandler   *admin.Handler
	oauthIdle      *admin.OAuthIdleMaintainer
	userHandler    *user.Handler
	router         *Router
	quotaScheduler *quota.Scheduler
	webFS          fs.FS
}

type Option func(*Server)

func WithWebFS(webFS fs.FS) Option {
	return func(s *Server) {
		s.webFS = webFS
	}
}

func NewGateway(cfg *config.Config, database *gorm.DB, pools *relay.PoolManager, billing *relay.BillingService, userSvc *user.Service, cfgPath string, opts ...Option) *Server {
	affinity := relay.NewAffinityCache()
	cacheTTL, _ := time.ParseDuration(cfg.Gateway.CacheTTL)

	concLimiter := relay.NewConcurrencyLimiter(cfg.Server.ConcurrencyLimit)

	s := &Server{
		cfg:      cfg,
		cfgPath:  cfgPath,
		db:       database,
		pools:    pools,
		billing:  billing,
		affinity: affinity,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.quotaScheduler = quota.NewScheduler(database)
	// Set up quota bucket reset auto-recovery hook
	s.quotaScheduler.SetRecoveryHook(func(accountID string) {
		// Clear quota_exhausted auto-disable reason and refresh pool
		var acc db.Account
		if s.db.Where("id = ?", accountID).First(&acc).Error != nil {
			return
		}
		if acc.Metadata == nil {
			return
		}
		// Only clear if it's quota_exhausted (not terminal auth errors)
		if reason, _ := acc.Metadata["auto_disable_reason"].(string); reason == "quota_exhausted" {
			delete(acc.Metadata, "auto_disable_reason")
			delete(acc.Metadata, "auto_disable_time")
			s.db.Model(&acc).Update("metadata", acc.Metadata)
			// Refresh pool to re-enable the account - reload from DB
			if pools != nil {
				channelID := acc.ChannelID.String()
				if old, ok := pools.GetPool(channelID); ok {
					old.Close()
				}
				var accounts []*db.Account
				s.db.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", channelID).Find(&accounts)
				pools.SetPool(channelID, relay.NewAccountPool(accounts))
				logger.Infof("quota.recovery", "account auto-recovered after quota bucket reset",
					logger.F("account_id", accountID),
					logger.F("channel_id", channelID))
			}
		}
	})
	s.gateway = gateway.New(database, billing, unavailableRelay, cfg.Gateway.InternalSecret, cfg.Gateway.GatewayID, concLimiter, cacheTTL, cfg.Security.TrustedProxies, time.Duration(cfg.Server.StreamIdleTimeoutSeconds)*time.Second)
	refreshPool := makeRefreshPool(database, pools, nil)
	s.adminHandler = admin.NewHandler(database, cfg, cfgPath, refreshPool, makeRemovePool(pools, nil))
	s.adminHandler.SetQuotaScheduler(s.quotaScheduler)
	s.oauthIdle = admin.StartOAuthIdleMaintenance(database, refreshPool)
	s.adminHandler.OAuthIdle = s.oauthIdle
	s.userHandler = user.NewHandler(userSvc)
	s.userHandler.SetQueueStatusFunc(concLimiter.PerTokenStats)
	s.setupRoutes()
	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	maxBodySize := s.cfg.Server.MaxBodySizeMB * 1024 * 1024
	logger.Infof("server", "gateway listening", logger.F("addr", addr), logger.F("max_body_size_mb", s.cfg.Server.MaxBodySizeMB))

	server := &fasthttp.Server{
		Handler:            s.handler(),
		MaxRequestBodySize: maxBodySize,
	}
	return server.ListenAndServe(addr)
}

// Close gracefully shuts down server resources.
func (s *Server) Close() {
	if s.affinity != nil {
		s.affinity.Close()
	}
	if s.oauthIdle != nil {
		s.oauthIdle.Stop()
	}
}

func (s *Server) handler() fasthttp.RequestHandler {
	maxBodySize := s.cfg.Server.MaxBodySizeMB * 1024 * 1024

	return func(ctx *fasthttp.RequestCtx) {
		start := time.Now()
		path := string(ctx.Path())
		method := string(ctx.Method())
		debugTrace := startGatewayDebugDump(ctx, method, path, start)
		if path != "/healthz" {
			defer func() {
				if debugTrace != nil {
					debugTrace.Finish(ctx)
				}
				logger.Debugf("server.request", "request completed",
					logger.F("method", method),
					logger.F("path", path),
					logger.F("status", ctx.Response.StatusCode()),
					logger.F("latency_ms", time.Since(start).Milliseconds()),
					logger.F("body_bytes", len(ctx.PostBody())),
					logger.F("remote_ip", ctx.RemoteIP().String()),
				)
			}()
		}

		if path == "/healthz" {
			ctx.SetContentType("application/json")
			ctx.SetBodyString(`{"status":"ok"}`)
			return
		}

		// Limit request body size
		if len(ctx.PostBody()) > maxBodySize {
			ctx.Error(`{"error":"request body too large"}`, fasthttp.StatusRequestEntityTooLarge)
			return
		}

		// Relay paths — shortest path, no router/middleware
		if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v1beta/") {
			s.gateway.Handle(ctx)
			return
		}

		// API paths — use router
		if s.router == nil {
			ctx.SetStatusCode(404)
			ctx.SetBodyString(`{"code":404,"message":"not found"}`)
			return
		}
		h, params := s.router.Lookup(method, path)
		if h == nil {
			if s.serveWeb(ctx, method, path) {
				return
			}
			ctx.SetStatusCode(404)
			ctx.SetBodyString(`{"code":404,"message":"not found"}`)
			return
		}
		for k, v := range params {
			ctx.SetUserValue(k, v)
		}
		h(ctx)
	}
}

func (s *Server) serveWeb(ctx *fasthttp.RequestCtx, method, requestPath string) bool {
	if s.webFS == nil {
		return false
	}
	if method != fasthttp.MethodGet && method != fasthttp.MethodHead {
		return false
	}
	if isReservedWebPath(requestPath) {
		return false
	}
	fileName := webFileName(requestPath)
	body, contentType, ok := readWebFile(s.webFS, fileName)
	if !ok && !strings.HasSuffix(fileName, "/index.html") {
		body, contentType, ok = readWebFile(s.webFS, path.Join(fileName, "index.html"))
	}
	if !ok {
		body, contentType, ok = readWebFile(s.webFS, "index.html")
	}
	if !ok {
		return false
	}
	ctx.SetContentType(contentType)
	ctx.Response.Header.Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	ctx.Response.Header.Set("Pragma", "no-cache")
	ctx.Response.Header.Set("Expires", "0")
	if method != fasthttp.MethodHead {
		ctx.SetBody(body)
	}
	return true
}

func isReservedWebPath(requestPath string) bool {
	return strings.HasPrefix(requestPath, "/api/") ||
		strings.HasPrefix(requestPath, "/internal/") ||
		strings.HasPrefix(requestPath, "/v1/") ||
		strings.HasPrefix(requestPath, "/v1beta/") ||
		requestPath == "/v1" ||
		requestPath == "/v1beta"
}

func webFileName(requestPath string) string {
	clean := path.Clean("/" + strings.TrimSpace(requestPath))
	if clean == "/" || clean == "." {
		return "index.html"
	}
	name := strings.TrimPrefix(clean, "/")
	if strings.HasSuffix(requestPath, "/") {
		return path.Join(name, "index.html")
	}
	if path.Ext(name) == "" {
		return path.Join(name, "index.html")
	}
	return name
}

func readWebFile(webFS fs.FS, name string) ([]byte, string, bool) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if name == "." || strings.HasPrefix(name, "../") {
		return nil, "", false
	}
	body, err := fs.ReadFile(webFS, name)
	if err != nil {
		return nil, "", false
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return body, contentType, true
}

func (s *Server) setupRoutes() {
	r := NewRouter()

	// Admin auth (no JWT required)
	r.POST("/api/admin/login", s.adminHandler.HandleLogin)
	r.POST("/api/admin/refresh", s.adminHandler.HandleRefresh)
	r.GET("/api/admin/init-status", s.adminHandler.HandleInitStatus)
	r.POST("/api/admin/setup", s.adminHandler.HandleSetup)
	r.GET("/api/public/settings", s.adminHandler.HandlePublicSettings)
	r.GET("/api/public/wallpaper", s.adminHandler.HandlePublicWallpaper)
	r.GET("/api/admin/channels/oauth/callback", s.adminHandler.OAuthCallback)
	r.GET("/internal/config", s.handleInternalAuth(s.adminHandler.RelayConfig))
	r.POST("/internal/usage", s.handleInternalAuth(s.adminHandler.UsageEvent))
	r.POST("/internal/account", s.handleInternalAuth(s.adminHandler.RelayAccountUpdate))
	r.POST("/internal/dumps", s.handleInternalAuth(s.adminHandler.RemoteDebugDumps))

	// Admin CRUD (JWT checked inside handleAdminAuth + individual handlers)
	r.GET("/api/admin/dashboard", s.handleAdminAuth(s.adminHandler.HandleDashboard))
	r.GET("/api/admin/access-policies", s.handleAdminAuth(s.adminHandler.HandleAccessPolicies))
	r.POST("/api/admin/access-policies", s.handleAdminAuth(s.adminHandler.HandleAccessPolicies))
	r.PUT("/api/admin/access-policies", s.handleAdminAuth(s.adminHandler.HandleAccessPolicies))
	r.DELETE("/api/admin/access-policies", s.handleAdminAuth(s.adminHandler.HandleAccessPolicies))
	r.GET("/api/admin/relay-nodes", s.handleAdminAuth(s.adminHandler.HandleRelayNodes))
	r.POST("/api/admin/relay-nodes", s.handleAdminAuth(s.adminHandler.HandleRelayNodes))
	r.PUT("/api/admin/relay-nodes", s.handleAdminAuth(s.adminHandler.HandleRelayNodes))
	r.DELETE("/api/admin/relay-nodes", s.handleAdminAuth(s.adminHandler.HandleRelayNodes))
	r.GET("/api/admin/node-channels", s.handleAdminAuth(s.adminHandler.HandleNodeChannels))
	r.POST("/api/admin/node-channels", s.handleAdminAuth(s.adminHandler.HandleNodeChannels))
	r.PUT("/api/admin/node-channels", s.handleAdminAuth(s.adminHandler.HandleNodeChannels))
	r.DELETE("/api/admin/node-channels", s.handleAdminAuth(s.adminHandler.HandleNodeChannels))
	r.POST("/api/admin/channels/oauth/auth-url", s.handleAdminAuth(s.adminHandler.StartOAuth))
	r.POST("/api/admin/channels/oauth/complete", s.handleAdminAuth(s.adminHandler.CompleteOAuth))
	r.GET("/api/admin/channels/oauth/status", s.handleAdminAuth(s.adminHandler.OAuthStatus))
	r.POST("/api/admin/channels/oauth/bind", s.handleAdminAuth(s.adminHandler.BindOAuthAccount))
	r.POST("/api/admin/channels/reverse/auth-url", s.handleAdminAuth(s.adminHandler.StartReverseAuth))
	r.POST("/api/admin/channels/reverse/complete", s.handleAdminAuth(s.adminHandler.CompleteReverseAuth))
	r.GET("/api/admin/channels/catalog", s.handleAdminAuth(s.adminHandler.HandleChannelCatalog))
	r.POST("/api/admin/channels/models/sync", s.handleAdminAuth(s.adminHandler.HandleChannelModelSync))
	r.GET("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.POST("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.PUT("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.DELETE("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.POST("/api/admin/accounts/export", s.handleAdminAuth(s.adminHandler.HandleAccountCredentialExport))
	r.POST("/api/admin/accounts/:id/refresh-quota", s.handleAdminAuth(s.adminHandler.HandleRefreshAccountQuota))
	r.POST("/api/admin/channels/:id/refresh-quota", s.handleAdminAuth(s.adminHandler.HandleRefreshChannelQuota))
	r.POST("/api/admin/channels/:id/delete-auth-failed-accounts", s.handleAdminAuth(s.adminHandler.HandleDeleteAuthFailedAccounts))
	r.GET("/api/admin/accounts", s.handleAdminAuth(s.adminHandler.HandleAccounts))
	r.POST("/api/admin/accounts", s.handleAdminAuth(s.adminHandler.HandleAccounts))
	r.PUT("/api/admin/accounts", s.handleAdminAuth(s.adminHandler.HandleAccounts))
	r.DELETE("/api/admin/accounts", s.handleAdminAuth(s.adminHandler.HandleAccounts))
	r.GET("/api/admin/tokens", s.handleAdminAuth(s.adminHandler.HandleTokens))
	r.POST("/api/admin/tokens", s.handleAdminAuth(s.adminHandler.HandleTokens))
	r.PUT("/api/admin/tokens", s.handleAdminAuth(s.adminHandler.HandleTokens))
	r.DELETE("/api/admin/tokens", s.handleAdminAuth(s.adminHandler.HandleTokens))
	r.GET("/api/admin/plans", s.handleAdminAuth(s.adminHandler.HandlePlans))
	r.POST("/api/admin/plans", s.handleAdminAuth(s.adminHandler.HandlePlans))
	r.PUT("/api/admin/plans", s.handleAdminAuth(s.adminHandler.HandlePlans))
	r.DELETE("/api/admin/plans", s.handleAdminAuth(s.adminHandler.HandlePlans))
	r.GET("/api/admin/logs", s.handleAdminAuth(s.adminHandler.HandleLogs))
	r.GET("/api/admin/audit-logs", s.handleAdminAuth(s.adminHandler.ListAuditLogs))
	r.GET("/api/admin/settings", s.handleAdminAuth(s.adminHandler.HandleSettings))
	r.PUT("/api/admin/settings", s.handleAdminAuth(s.adminHandler.HandleSettings))
	r.POST("/api/admin/settings/export", s.handleAdminAuth(s.adminHandler.HandleSettingsExport))
	r.POST("/api/admin/settings/import", s.handleAdminAuth(s.adminHandler.HandleSettingsImport))
	r.POST("/api/admin/settings/wallpaper", s.handleAdminAuth(s.adminHandler.HandleWallpaperUpload))
	r.GET("/api/admin/redeem-codes", s.handleAdminAuth(s.adminHandler.HandleRedeemCodes))
	r.POST("/api/admin/redeem-codes", s.handleAdminAuth(s.adminHandler.HandleRedeemCodes))
	r.DELETE("/api/admin/redeem-codes", s.handleAdminAuth(s.adminHandler.HandleRedeemCodes))
	r.GET("/api/admin/users", s.handleAdminAuth(s.adminHandler.ListUsers))
	r.POST("/api/admin/users/export", s.handleAdminAuth(s.adminHandler.HandleUsersExport))
	r.POST("/api/admin/users/import", s.handleAdminAuth(s.adminHandler.HandleUsersImport))
	r.PUT("/api/admin/users", s.handleAdminAuth(s.adminHandler.UpdateUser))
	r.DELETE("/api/admin/users", s.handleAdminAuth(s.adminHandler.DeleteUser))

	// User auth (no JWT required)
	r.POST("/api/user/register", s.userHandler.Register)
	r.POST("/api/user/login", s.userHandler.Login)
	r.POST("/api/user/refresh", s.userHandler.RefreshToken)

	// User routes (JWT required)
	userAuth := auth.RequireUser(s.cfg.Security.JWTSecret)
	r.GET("/api/user/profile", userAuth(s.userHandler.GetProfile))
	r.POST("/api/user/password", userAuth(s.userHandler.UpdatePassword))
	r.POST("/api/user/email", userAuth(s.userHandler.UpdateEmail))
	r.GET("/api/user/keys", userAuth(s.userHandler.ListKeys))
	r.POST("/api/user/keys", userAuth(s.userHandler.CreateKey))
	r.DELETE("/api/user/keys/:keyID", userAuth(s.userHandler.DeleteKey))
	r.GET("/api/user/usage", userAuth(s.userHandler.GetUsage))
	r.GET("/api/user/usage/logs", userAuth(s.userHandler.GetUsageLogs))
	r.GET("/api/user/subscription", userAuth(s.userHandler.GetSubscription))
	r.GET("/api/user/queue-status", userAuth(s.userHandler.GetQueueStatus))
	r.GET("/api/user/plans", userAuth(s.userHandler.ListPlans))
	r.GET("/api/user/models", userAuth(s.userHandler.AvailableModels))
	r.POST("/api/user/redeem", userAuth(s.userHandler.RedeemCode))

	s.router = r
}

func unavailableRelay(ctx *fasthttp.RequestCtx) {
	ctx.Error(`{"error":"no relay route available"}`, fasthttp.StatusServiceUnavailable)
}

func isWebSocketUpgrade(ctx *fasthttp.RequestCtx) bool {
	upgrade := string(ctx.Request.Header.Peek("Upgrade"))
	if !strings.EqualFold(upgrade, "websocket") {
		return false
	}
	connection := strings.ToLower(string(ctx.Request.Header.Peek("Connection")))
	return strings.Contains(connection, "upgrade")
}

func (s *Server) handleInternalAuth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		secret := string(ctx.Request.Header.Peek("X-UAPI-Internal-Secret"))
		if secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(s.cfg.Gateway.InternalSecret)) != 1 {
			ctx.SetStatusCode(fasthttp.StatusUnauthorized)
			ctx.SetBodyString(`{"code":401,"message":"unauthorized"}`)
			return
		}
		next(ctx)
	}
}

// handleAdminAuth wraps an admin handler with CORS + JWT auth check.
// Sets the admin username in the context for audit logging.
func (s *Server) handleAdminAuth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		origin := string(ctx.Request.Header.Peek("Origin"))
		if origin != "" {
			if !s.originAllowed(origin) {
				ctx.Error(`{"code":403,"message":"origin not allowed"}`, fasthttp.StatusForbidden)
				return
			}
			ctx.Response.Header.Set("Access-Control-Allow-Origin", origin)
			ctx.Response.Header.Set("Access-Control-Allow-Credentials", "true")
		}
		ctx.Response.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		ctx.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if string(ctx.Method()) == "OPTIONS" {
			ctx.SetStatusCode(204)
			return
		}
		username, ok := s.adminHandler.RequireAuthWithUser(ctx)
		if !ok {
			return
		}
		ctx.SetUserValue("admin_user", username)
		next(ctx)
	}
}

func (s *Server) originAllowed(origin string) bool {
	allowed := s.cfg.Server.AllowedOrigins
	if len(allowed) == 0 {
		return true
	}
	for _, o := range allowed {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

// Helper functions

// makeRefreshPool returns a callback that reloads accounts for a channel from DB and updates the pool.
func makeRefreshPool(database *gorm.DB, pools *relay.PoolManager, invalidateChannels func()) func(channelID string) {
	return func(channelID string) {
		if invalidateChannels != nil {
			invalidateChannels()
		}
		if old, ok := pools.GetPool(channelID); ok {
			old.Close()
		}
		var accounts []*db.Account
		database.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", channelID).Find(&accounts)
		pools.SetPool(channelID, relay.NewAccountPool(accounts))
	}
}

// makeRemovePool returns a callback that removes a channel's pool.
func makeRemovePool(pools *relay.PoolManager, invalidateChannels func()) func(channelID string) {
	return func(channelID string) {
		if invalidateChannels != nil {
			invalidateChannels()
		}
		pools.RemovePool(channelID)
	}
}
