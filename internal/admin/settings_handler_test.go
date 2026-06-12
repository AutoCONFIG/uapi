package admin

import (
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/valyala/fasthttp"
)

func TestHandleSettingsRejectsLargePayloadThresholdAboveMaxBodySize(t *testing.T) {
	h := NewHandler(nil, &config.Config{Server: config.ServerConfig{MaxBodySizeMB: 256}}, "", nil, nil)

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPut)
	ctx.Request.SetRequestURI("/api/admin/settings")
	ctx.Request.SetBodyString(`{"large_payload_threshold_mb":257}`)

	h.HandleSettings(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("status = %d, want %d", ctx.Response.StatusCode(), fasthttp.StatusBadRequest)
	}
	if body := string(ctx.Response.Body()); !strings.Contains(body, "between 1 and 256") {
		t.Fatalf("body = %s, want max body size validation", body)
	}
}
