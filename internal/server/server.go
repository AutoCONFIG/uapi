package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/admin"
	"github.com/AutoCONFIG/cli-relay/internal/auth"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay"
	"github.com/AutoCONFIG/cli-relay/internal/user"
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
	adminHandler *admin.Handler
	userHandler  *user.Handler
	router       *Router
}

func New(cfg *config.Config, database *gorm.DB, pools *relay.PoolManager, billing *relay.BillingService, userSvc *user.Service, cfgPath string) *Server {
	s := &Server{
		cfg:          cfg,
		cfgPath:      cfgPath,
		db:           database,
		pools:        pools,
		billing:      billing,
		relayer:      relay.NewRelayer(database, pools, billing, relay.NewAffinityCache(), cfg.Server.ConcurrencyLimit),
		adminHandler: admin.NewHandler(database, cfg, cfgPath, makeRefreshPool(database, pools), makeRemovePool(pools)),
		userHandler:  user.NewHandler(userSvc),
	}
	s.setupRoutes()
	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	log.Printf("server listening on %s", addr)

	handler := s.handler()
	return fasthttp.ListenAndServe(addr, handler)
}

func (s *Server) handler() fasthttp.RequestHandler {
	maxBodySize := s.cfg.Server.MaxBodySizeMB * 1024 * 1024

	return func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Path())

		// Limit request body size
		if len(ctx.PostBody()) > maxBodySize {
			ctx.Error(`{"error":"request body too large"}`, fasthttp.StatusRequestEntityTooLarge)
			return
		}

		// Relay paths — shortest path, no router/middleware
		if strings.HasPrefix(path, "/v1/") {
			s.relayer.HandleRelay(ctx)
			return
		}

		// API paths — use router
		method := string(ctx.Method())
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
	r := NewRouter()

	// Admin auth (no JWT required)
	r.POST("/api/admin/login", s.adminHandler.HandleLogin)
	r.GET("/api/admin/init-status", s.adminHandler.HandleInitStatus)
	r.POST("/api/admin/setup", s.adminHandler.HandleSetup)
	r.GET("/api/admin/channels/oauth/callback", s.adminHandler.OAuthCallback)

	// Admin CRUD (JWT checked inside handleAdminAuth + individual handlers)
	r.GET("/api/admin/dashboard", s.handleAdminAuth(s.adminHandler.HandleDashboard))
	r.POST("/api/admin/channels/oauth/auth-url", s.handleAdminAuth(s.adminHandler.StartOAuth))
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
