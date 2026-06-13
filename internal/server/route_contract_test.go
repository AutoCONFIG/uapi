package server

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/admin"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/user"
)

func TestAPIRoutesRegisteredAndLookupable(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{MaxBodySizeMB: 256},
		Security: config.SecurityConfig{
			JWTSecret:     strings.Repeat("j", 32),
			EncryptionKey: strings.Repeat("a", 64),
		},
		Gateway: config.GatewayConfig{InternalSecret: "secret"},
	}
	s := &Server{
		cfg:          cfg,
		adminHandler: admin.NewHandler(nil, cfg, "", func(string) {}, func(string) {}),
		userHandler:  user.NewHandler(user.NewService(nil, cfg.Security.JWTSecret, time.Minute, time.Hour)),
	}
	s.setupRoutes()

	var got []string
	for _, rt := range s.router.routes {
		got = append(got, rt.method+" "+rt.path)
		path := sampleRoutePath(rt.path)
		if h, _ := s.router.Lookup(rt.method, path); h == nil {
			t.Fatalf("registered route does not resolve: %s %s sampled as %s", rt.method, rt.path, path)
		}
	}
	sort.Strings(got)

	want := []string{
		"DELETE /api/admin/access-policies",
		"DELETE /api/admin/accounts",
		"DELETE /api/admin/channels",
		"DELETE /api/admin/node-channels",
		"DELETE /api/admin/plans",
		"DELETE /api/admin/redeem-codes",
		"DELETE /api/admin/relay-nodes",
		"DELETE /api/admin/tokens",
		"DELETE /api/admin/users",
		"DELETE /api/user/keys/:keyID",
		"GET /api/admin/access-policies",
		"GET /api/admin/accounts",
		"GET /api/admin/audit-logs",
		"GET /api/admin/channels",
		"GET /api/admin/channels/catalog",
		"GET /api/admin/channels/oauth/callback",
		"GET /api/admin/channels/oauth/status",
		"GET /api/admin/dashboard",
		"GET /api/admin/init-status",
		"GET /api/admin/logs",
		"GET /api/admin/node-channels",
		"GET /api/admin/plans",
		"GET /api/admin/redeem-codes",
		"GET /api/admin/relay-nodes",
		"GET /api/admin/settings",
		"GET /api/admin/tokens",
		"GET /api/admin/users",
		"GET /api/public/settings",
		"GET /api/public/wallpaper",
		"GET /api/user/keys",
		"GET /api/user/models",
		"GET /api/user/plans",
		"GET /api/user/profile",
		"GET /api/user/queue-status",
		"GET /api/user/subscription",
		"GET /api/user/usage",
		"GET /api/user/usage/logs",
		"GET /internal/config",
		"POST /api/admin/access-policies",
		"POST /api/admin/accounts",
		"POST /api/admin/accounts/:id/refresh-quota",
		"POST /api/admin/accounts/export",
		"POST /api/admin/channels",
		"POST /api/admin/channels/:id/delete-auth-failed-accounts",
		"POST /api/admin/channels/:id/refresh-quota",
		"POST /api/admin/channels/models/sync",
		"POST /api/admin/channels/oauth/auth-url",
		"POST /api/admin/channels/oauth/bind",
		"POST /api/admin/channels/oauth/complete",
		"POST /api/admin/channels/reverse/auth-url",
		"POST /api/admin/channels/reverse/complete",
		"POST /api/admin/login",
		"POST /api/admin/node-channels",
		"POST /api/admin/plans",
		"POST /api/admin/redeem-codes",
		"POST /api/admin/refresh",
		"POST /api/admin/relay-nodes",
		"POST /api/admin/settings/export",
		"POST /api/admin/settings/import",
		"POST /api/admin/settings/wallpaper",
		"POST /api/admin/setup",
		"POST /api/admin/tokens",
		"POST /api/admin/users/export",
		"POST /api/admin/users/import",
		"POST /api/user/email",
		"POST /api/user/keys",
		"POST /api/user/login",
		"POST /api/user/password",
		"POST /api/user/redeem",
		"POST /api/user/refresh",
		"POST /api/user/register",
		"POST /internal/account",
		"POST /internal/dumps",
		"POST /internal/usage",
		"PUT /api/admin/access-policies",
		"PUT /api/admin/accounts",
		"PUT /api/admin/channels",
		"PUT /api/admin/node-channels",
		"PUT /api/admin/plans",
		"PUT /api/admin/relay-nodes",
		"PUT /api/admin/settings",
		"PUT /api/admin/tokens",
		"PUT /api/admin/users",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered API routes changed\n got: %#v\nwant: %#v", got, want)
	}
}

func sampleRoutePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = "sample-id"
		}
	}
	return strings.Join(parts, "/")
}
