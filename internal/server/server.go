package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/admin"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/relay"
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
	// userHandler will be added in Task 4
	router *Router
}

func New(cfg *config.Config, database *gorm.DB, pools *relay.PoolManager, billing *relay.BillingService, cfgPath string) *Server {
	s := &Server{
		cfg:          cfg,
		cfgPath:      cfgPath,
		db:           database,
		pools:        pools,
		billing:      billing,
		relayer:      relay.NewRelayer(database, pools, billing, relay.NewAffinityCache(), cfg.Server.ConcurrencyLimit),
		adminHandler: admin.NewHandler(database, cfg, cfgPath),
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

	// Admin CRUD (JWT checked inside handleAdminAuth + individual handlers)
	r.GET("/api/admin/dashboard", s.handleAdminAuth(s.adminHandler.HandleDashboard))
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

	// User routes will be added in Task 4/8 when userHandler is wired in
	// For now, return 404 for user API paths

	s.router = r
}

// handleAdminAuth wraps an admin handler with CORS + JWT auth check.
func (s *Server) handleAdminAuth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		// CORS
		ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
		ctx.Response.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		ctx.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if string(ctx.Method()) == "OPTIONS" {
			ctx.SetStatusCode(204)
			return
		}
		if !s.adminHandler.RequireAuth(ctx) {
			return
		}
		next(ctx)
	}
}

// Helper functions
func jsonResponse(ctx *fasthttp.RequestCtx, status int, data interface{}) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(status)
	body, _ := json.Marshal(map[string]interface{}{
		"code":    0,
		"data":    data,
		"message": "ok",
	})
	ctx.SetBody(body)
}

func jsonError(ctx *fasthttp.RequestCtx, status int, msg string) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(status)
	body, _ := json.Marshal(map[string]interface{}{
		"code":    status,
		"message": msg,
	})
	ctx.SetBody(body)
}
