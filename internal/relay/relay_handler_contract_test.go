package relay_test

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/httputil"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/valyala/fasthttp"
)

func TestClientIPCandidatesIncludesProxyHeaders(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Real-IP", "203.0.113.10")
	ctx.Request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.2")

	got := httputil.ClientIPCandidates(&ctx, nil)
	if len(got) != 1 {
		t.Fatalf("expected only remote IP without trusted proxies, got %#v", got)
	}

	got = httputil.ClientIPCandidates(&ctx, []string{"0.0.0.0"})
	if len(got) < 3 {
		t.Fatalf("expected remote IP plus proxy headers, got %#v", got)
	}
	if got[1] != "203.0.113.10" {
		t.Fatalf("X-Real-IP candidate mismatch: %#v", got)
	}
	if got[2] != "198.51.100.8" {
		t.Fatalf("X-Forwarded-For first-hop candidate mismatch: %#v", got)
	}
}

func TestStreamToNonStreamPreservesErrorChunk(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-test","object":"error","error":{"message":"upstream failed","type":"conversion_error"}}` + "\n\n")
	out := string(relay.StreamToNonStream(body))
	if out != `{"error":{"message":"upstream failed","type":"conversion_error"},"id":"chatcmpl-test","object":"error"}` {
		t.Fatalf("error chunk must not be converted to empty chat completion: %s", out)
	}
}
