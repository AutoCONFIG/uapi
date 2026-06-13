package relayserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/valyala/fasthttp"
)

type Server struct {
	cfg     *config.Config
	relayer *relay.Relayer
	nodeID  string
}

func New(cfg *config.Config, relayer *relay.Relayer, nodeID string) *Server {
	return &Server{cfg: cfg, relayer: relayer, nodeID: strings.TrimSpace(nodeID)}
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	maxBodySize := s.cfg.Server.MaxBodySizeMB * 1024 * 1024
	logger.Infof("relay.server", "relay listening", logger.F("addr", addr), logger.F("max_body_size_mb", s.cfg.Server.MaxBodySizeMB))

	server := &fasthttp.Server{
		Handler:            s.handler(),
		MaxRequestBodySize: maxBodySize,
	}
	return server.ListenAndServe(addr)
}

func (s *Server) handler() fasthttp.RequestHandler {
	maxBodySize := s.cfg.Server.MaxBodySizeMB * 1024 * 1024

	return func(ctx *fasthttp.RequestCtx) {
		start := time.Now()
		path := string(ctx.Path())
		method := string(ctx.Method())
		if path != "/healthz" {
			defer func() {
				logger.Debugf("relay.server.request", "request completed",
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
		if len(ctx.PostBody()) > maxBodySize {
			ctx.Error(`{"error":"request body too large"}`, fasthttp.StatusRequestEntityTooLarge)
			return
		}
		if method == fasthttp.MethodPost && path == "/internal/execute" {
			s.handleExecute(ctx)
			return
		}
		if method == fasthttp.MethodPost && path == "/internal/reload" {
			s.handleReload(ctx)
			return
		}
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetBodyString(`{"code":404,"message":"not found"}`)
	}
}

func (s *Server) handleExecute(ctx *fasthttp.RequestCtx) {
	s.relayer.HandleRelay(ctx)
}

func (s *Server) handleReload(ctx *fasthttp.RequestCtx) {
	secret := strings.TrimSpace(string(ctx.Request.Header.Peek("X-UAPI-Internal-Secret")))
	if secret == "" || secret != strings.TrimSpace(s.cfg.Gateway.InternalSecret) {
		ctx.SetStatusCode(fasthttp.StatusUnauthorized)
		ctx.SetBodyString(`{"code":401,"message":"unauthorized"}`)
		return
	}
	if s.nodeID == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"code":400,"message":"missing relay node id"}`)
		return
	}
	ok := s.relayer.TriggerConfigPull(s.nodeID)
	ctx.SetContentType("application/json")
	if ok {
		ctx.SetBodyString(`{"ok":true}`)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusBadGateway)
	ctx.SetBodyString(`{"ok":false}`)
}
