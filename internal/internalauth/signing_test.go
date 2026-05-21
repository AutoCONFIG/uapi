package internalauth

import (
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

func TestSignAndVerifyRequest(t *testing.T) {
	secret := "test-secret"
	now := time.Unix(1000, 0)
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/v1/chat/completions")
	req.SetBodyString(`{"model":"gpt-test"}`)
	claims := Claims{GatewayID: "gw-1", TokenID: "token-1", UserID: "user-1", Model: "gpt-test", EstimatedTokens: 1000, Precharged: true, ClientIP: "127.0.0.1"}
	if err := SignRequest(&req, secret, claims, now); err != nil {
		t.Fatalf("SignRequest() error = %v", err)
	}

	var ctx fasthttp.RequestCtx
	req.CopyTo(&ctx.Request)
	got, ok := VerifyRequest(&ctx, secret, now)
	if !ok {
		t.Fatal("VerifyRequest() rejected signed request")
	}
	if got.TokenID != claims.TokenID || got.Model != claims.Model || !got.Precharged || got.EstimatedTokens != claims.EstimatedTokens {
		t.Fatalf("VerifyRequest() claims = %+v, want %+v", got, claims)
	}
}

func TestVerifyRequestRejectsBodyTamper(t *testing.T) {
	secret := "test-secret"
	now := time.Unix(1000, 0)
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/v1/chat/completions")
	req.SetBodyString(`{"model":"gpt-test"}`)
	claims := Claims{GatewayID: "gw-1", TokenID: "token-1", Model: "gpt-test", EstimatedTokens: 1000, Precharged: true}
	if err := SignRequest(&req, secret, claims, now); err != nil {
		t.Fatalf("SignRequest() error = %v", err)
	}
	req.SetBodyString(`{"model":"other"}`)

	var ctx fasthttp.RequestCtx
	req.CopyTo(&ctx.Request)
	if _, ok := VerifyRequest(&ctx, secret, now); ok {
		t.Fatal("VerifyRequest() accepted tampered body")
	}
}
