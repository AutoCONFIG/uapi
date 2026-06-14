package server

import (
	"testing"

	"github.com/valyala/fasthttp"
)

func TestUnavailableRelayReturnsServiceUnavailable(t *testing.T) {
	var ctx fasthttp.RequestCtx
	unavailableRelay(&ctx)

	if got := ctx.Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", got, fasthttp.StatusServiceUnavailable)
	}
	if got := string(ctx.Response.Body()); got != `{"error":"no relay route available"}` {
		t.Fatalf("body = %q", got)
	}
}
