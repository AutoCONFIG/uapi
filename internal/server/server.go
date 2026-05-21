package server

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/admin"
	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/gateway"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/AutoCONFIG/uapi/internal/user"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type Server struct {
	cfg          *config.Config
	cfgPath      string
	db           *gorm.DB
	pools        *relay.PoolManager
	billing      *relay.BillingService
	relayer      *relay.Relayer
	gateway      *gateway.Gateway
	affinity     *relay.AffinityCache
	adminHandler *admin.Handler
	oauthIdle    *admin.OAuthIdleMaintainer
	userHandler  *user.Handler
	router       *Router
}

func New(cfg *config.Config, database *gorm.DB, pools *relay.PoolManager, billing *relay.BillingService, userSvc *user.Service, cfgPath string) *Server {
	affinity := relay.NewAffinityCache()
	cacheTTL, _ := time.ParseDuration(cfg.Gateway.CacheTTL)
	pullInterval, _ := time.ParseDuration(cfg.Gateway.ConfigPullInterval)
	s := &Server{
		cfg:      cfg,
		cfgPath:  cfgPath,
		db:       database,
		pools:    pools,
		billing:  billing,
		affinity: affinity,
	}
	if cfg.Server.Mode == "all" || cfg.Server.Mode == "relay" {
		s.relayer = relay.NewRelayer(database, pools, billing, affinity, cfg.Server.ConcurrencyLimit, cfg.Gateway.InternalSecret, cfg.Gateway.RequireInternal, cfg.Gateway.ControlURL)
		s.relayer.StartConfigPuller(cfg.Gateway.RelayNodeID, pullInterval)
	}
	if cfg.Server.Mode == "all" || cfg.Server.Mode == "gateway" {
		fallback := unavailableRelay
		if s.relayer != nil {
			fallback = s.relayer.HandleRelay
		}
		s.gateway = gateway.New(database, billing, fallback, cfg.Gateway.InternalSecret, cfg.Gateway.GatewayID, cfg.Server.ConcurrencyLimit, cacheTTL)
		refreshPool := makeRefreshPool(database, pools)
		s.adminHandler = admin.NewHandler(database, cfg, cfgPath, refreshPool, makeRemovePool(pools))
		s.oauthIdle = admin.StartOAuthIdleMaintenance(database, refreshPool)
		s.adminHandler.OAuthIdle = s.oauthIdle
		s.userHandler = user.NewHandler(userSvc)
	}
	s.setupRoutes()
	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	logger.Infof("server", "server listening", logger.F("addr", addr), logger.F("mode", s.cfg.Server.Mode))

	handler := s.handler()
	return fasthttp.ListenAndServe(addr, handler)
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
		if path != "/healthz" {
			defer func() {
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
			if s.cfg.Server.Mode == "relay" {
				s.relayer.HandleRelay(ctx)
			} else {
				s.gateway.Handle(ctx)
			}
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

func (s *Server) setupRoutes() {
	if s.cfg.Server.Mode == "relay" {
		s.router = NewRouter()
		return
	}
	r := NewRouter()

	// Admin auth (no JWT required)
	r.POST("/api/admin/login", s.adminHandler.HandleLogin)
	r.GET("/api/admin/init-status", s.adminHandler.HandleInitStatus)
	r.POST("/api/admin/setup", s.adminHandler.HandleSetup)
	r.GET("/api/admin/channels/oauth/callback", s.adminHandler.OAuthCallback)
	r.GET("/internal/relay/config", s.handleInternalAuth(s.adminHandler.RelayConfig))
	r.POST("/internal/relay/usage-events", s.handleInternalAuth(s.adminHandler.UsageEvent))

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
	r.GET("/api/admin/node-accounts", s.handleAdminAuth(s.adminHandler.HandleNodeAccounts))
	r.POST("/api/admin/node-accounts", s.handleAdminAuth(s.adminHandler.HandleNodeAccounts))
	r.PUT("/api/admin/node-accounts", s.handleAdminAuth(s.adminHandler.HandleNodeAccounts))
	r.DELETE("/api/admin/node-accounts", s.handleAdminAuth(s.adminHandler.HandleNodeAccounts))
	r.POST("/api/admin/channels/oauth/auth-url", s.handleAdminAuth(s.adminHandler.StartOAuth))
	r.POST("/api/admin/channels/oauth/complete", s.handleAdminAuth(s.adminHandler.CompleteOAuth))
	r.GET("/api/admin/channels/oauth/status", s.handleAdminAuth(s.adminHandler.OAuthStatus))
	r.POST("/api/admin/channels/oauth/bind", s.handleAdminAuth(s.adminHandler.BindOAuthAccount))
	r.GET("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.POST("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.PUT("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
	r.DELETE("/api/admin/channels", s.handleAdminAuth(s.adminHandler.HandleChannels))
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
	r.GET("/api/admin/users", s.handleAdminAuth(s.adminHandler.ListUsers))
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
	r.POST("/api/user/subscription/:planID", userAuth(s.userHandler.Subscribe))
	r.POST("/api/user/redeem", userAuth(s.userHandler.RedeemCode))
	r.GET("/api/user/plans", userAuth(s.userHandler.ListPlans))

	s.router = r
}

func unavailableRelay(ctx *fasthttp.RequestCtx) {
	ctx.Error(`{"error":"no relay route available"}`, fasthttp.StatusServiceUnavailable)
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
		// CORS — admin panel is same-origin, no wildcard
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

// Helper functions

// makeRefreshPool returns a callback that reloads accounts for a channel from DB and updates the pool.
func makeRefreshPool(database *gorm.DB, pools *relay.PoolManager) func(channelID string) {
	return func(channelID string) {
		if old, ok := pools.GetPool(channelID); ok {
			old.Close()
		}
		var accounts []*db.Account
		database.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", channelID).Find(&accounts)
		pools.SetPool(channelID, relay.NewAccountPool(accounts))
	}
}

// makeRemovePool returns a callback that removes a channel's pool.
func makeRemovePool(pools *relay.PoolManager) func(channelID string) {
	return func(channelID string) {
		pools.RemovePool(channelID)
	}
}
