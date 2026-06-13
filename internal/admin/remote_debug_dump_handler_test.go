package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/valyala/fasthttp"
)

func TestRemoteDebugDumpsWritesUploadUnderRelayNode(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(nil, &config.Config{
		DebugDump: config.DebugDumpConfig{
			AcceptRemote:     true,
			Dir:              dir,
			MaxUploadBytesMB: 1,
		},
	}, "", nil, nil)

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI("/internal/dumps")
	ctx.Request.Header.Set("X-UAPI-Relay-Node-ID", "../relay-1")
	ctx.Request.Header.Set("X-UAPI-Dump-ID", "../dump-1")
	ctx.Request.SetBody([]byte("archive"))

	h.RemoteDebugDumps(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d body = %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*", "relay", "relay-1", "archives", "*-dump-1.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %v, want one uploaded dump", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "archive" {
		t.Fatalf("body = %q, want archive", body)
	}
}

func TestRemoteDebugDumpsDisabled(t *testing.T) {
	h := NewHandler(nil, &config.Config{}, "", nil, nil)

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI("/internal/dumps")
	ctx.Request.SetBody([]byte("archive"))

	h.RemoteDebugDumps(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("status = %d, want 404", ctx.Response.StatusCode())
	}
	if !strings.Contains(string(ctx.Response.Body()), "remote dumps disabled") {
		t.Fatalf("body = %s, want disabled message", ctx.Response.Body())
	}
}
