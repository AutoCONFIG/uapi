package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/AutoCONFIG/cli-relay/internal/admin"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/relay"
	"github.com/AutoCONFIG/cli-relay/internal/web"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type Server struct {
	cfg     *config.Config
	db      *gorm.DB
	pools   *relay.PoolManager
	billing *relay.BillingService
	relayer *relay.Relayer
	admin   *admin.Handler
}

func New(cfg *config.Config, database *gorm.DB, pools *relay.PoolManager, billing *relay.BillingService) *Server {
	return &Server{
		cfg:     cfg,
		db:      database,
		pools:   pools,
		billing: billing,
		relayer: relay.NewRelayer(database, pools, billing),
		admin:   admin.NewHandler(database, cfg),
	}
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

		switch {
		case path == "/" || path == "/index.html":
			s.handleStatic(ctx)
		case strings.HasPrefix(path, "/api/admin/"):
			s.handleAdmin(ctx)
		case strings.HasPrefix(path, "/v1/"):
			s.relayer.HandleRelay(ctx)
		default:
			ctx.Error(`{"error":"not found"}`, fasthttp.StatusNotFound)
		}
	}
}

func (s *Server) handleStatic(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetBody(web.IndexHTML)
}

func (s *Server) handleAdmin(ctx *fasthttp.RequestCtx) {
	// CORS headers
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.Response.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	ctx.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if string(ctx.Method()) == "OPTIONS" {
		ctx.SetStatusCode(fasthttp.StatusNoContent)
		return
	}

	path := string(ctx.Path())

	// Login doesn't require auth
	if path == "/api/admin/login" {
		s.admin.HandleLogin(ctx)
		return
	}

	// All other admin routes require JWT
	if !s.admin.RequireAuth(ctx) {
		return
	}

	s.routeAdmin(ctx, path)
}

func (s *Server) routeAdmin(ctx *fasthttp.RequestCtx, path string) {
	switch {
	case strings.HasPrefix(path, "/api/admin/channels"):
		s.admin.HandleChannels(ctx)
	case strings.HasPrefix(path, "/api/admin/accounts"):
		s.admin.HandleAccounts(ctx)
	case strings.HasPrefix(path, "/api/admin/tokens"):
		s.admin.HandleTokens(ctx)
	case strings.HasPrefix(path, "/api/admin/plans"):
		s.admin.HandlePlans(ctx)
	case path == "/api/admin/logs":
		s.admin.HandleLogs(ctx)
	case path == "/api/admin/dashboard":
		s.admin.HandleDashboard(ctx)
	default:
		jsonError(ctx, fasthttp.StatusNotFound, "not found")
	}
}

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
