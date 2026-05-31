package httputil

import (
	"testing"

	"github.com/valyala/fasthttp"
)

func TestClientIPForGatewayLogUsesForwardedHeadersWithoutTrustedProxyConfig(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Real-IP", "203.0.113.10")
	ctx.Request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.2")

	got := ClientIPForGatewayLog(&ctx, nil)
	if got != "198.51.100.8" {
		t.Fatalf("ClientIPForGatewayLog() = %q, want first X-Forwarded-For hop", got)
	}
}

func TestClientIPForGatewayLogHonorsTrustedProxyConfig(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Forwarded-For", "198.51.100.8")

	got := ClientIPForGatewayLog(&ctx, []string{"10.0.0.1"})
	if got != "0.0.0.0" {
		t.Fatalf("ClientIPForGatewayLog() = %q, want remote IP from untrusted proxy", got)
	}
}

func TestClientIPForGatewayLogFallsBackToRemoteIP(t *testing.T) {
	var ctx fasthttp.RequestCtx

	got := ClientIPForGatewayLog(&ctx, nil)
	if got != "0.0.0.0" {
		t.Fatalf("ClientIPForGatewayLog() = %q, want remote IP", got)
	}
}
