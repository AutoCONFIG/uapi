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

func TestModelFromRequestPathExtractsGeminiModel(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		bodyModel string
		want      string
	}{
		{
			name: "generate content action",
			path: "/v1beta/models/gemini-2.5-pro:generateContent",
			want: "gemini-2.5-pro",
		},
		{
			name: "stream generate content action",
			path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			want: "gemini-2.5-pro",
		},
		{
			name: "slash suffix",
			path: "/v1beta/models/gemini-2.5-pro:generateContent/extra",
			want: "gemini-2.5-pro",
		},
		{
			name:      "body model wins",
			path:      "/v1beta/models/gemini-2.5-pro:generateContent",
			bodyModel: "body-model",
			want:      "body-model",
		},
		{
			name: "non gemini path without body model",
			path: "/v1/chat/completions",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModelFromRequestPath(tc.path, tc.bodyModel); got != tc.want {
				t.Fatalf("ModelFromRequestPath(%q, %q) = %q, want %q", tc.path, tc.bodyModel, got, tc.want)
			}
		})
	}
}
