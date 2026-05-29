package provider_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

func TestCodexAuthURLMatchesClientFingerprint(t *testing.T) {
	got := openai.BuildAuthURL("client id", "http://localhost:1455/auth/callback", "challenge", "state")
	query := strings.TrimPrefix(got, openai.DefaultAuthURL+"?")
	want := strings.Join([]string{
		"response_type=code",
		"client_id=client%20id",
		"redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback",
		"scope=openid%20profile%20email%20offline_access%20api.connectors.read%20api.connectors.invoke",
		"code_challenge=challenge",
		"code_challenge_method=S256",
		"id_token_add_organizations=true",
		"codex_cli_simplified_flow=true",
		"state=state",
		"originator=codex_cli_rs",
	}, "&")
	if query != want {
		t.Fatalf("query mismatch:\nwant %s\n got %s", want, query)
	}
	if strings.Contains(query, "+") {
		t.Fatalf("Codex URL should encode spaces as %%20: %s", query)
	}
}

func TestCodexVerifierAndUserAgentFingerprint(t *testing.T) {
	verifier, err := openai.GenerateCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 86 {
		t.Fatalf("Codex verifier should be 64 random bytes base64url length 86, got %d", len(verifier))
	}
	if !strings.HasPrefix(openai.CodexUserAgent, openai.CodexOriginator+"/0.0.0 (") ||
		!strings.HasSuffix(openai.CodexUserAgent, ") unknown") {
		t.Fatalf("unexpected Codex UA: %s", openai.CodexUserAgent)
	}
}

func TestClaudeCodeAuthURLMatchesClientFingerprint(t *testing.T) {
	got := anthropic.BuildAuthURL("client", anthropic.DefaultRedirectURI, "challenge", "state")
	query := strings.TrimPrefix(got, anthropic.DefaultAuthURL+"?")
	want := strings.Join([]string{
		"code=true",
		"client_id=client",
		"response_type=code",
		"redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback",
		"scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload",
		"code_challenge=challenge",
		"code_challenge_method=S256",
		"state=state",
	}, "&")
	if query != want {
		t.Fatalf("query mismatch:\nwant %s\n got %s", want, query)
	}
}

func TestClaudeCodeVerifierAndUserAgentFingerprint(t *testing.T) {
	verifier, err := anthropic.GenerateCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 43 {
		t.Fatalf("Claude verifier should be 32 random bytes base64url length 43, got %d", len(verifier))
	}
	if anthropic.ClaudeCLIUserAgent != "claude-cli/2.1.156 (external, cli)" {
		t.Fatalf("unexpected Claude CLI UA: %s", anthropic.ClaudeCLIUserAgent)
	}
	if anthropic.ClaudeCodeUserAgent != "claude-code/2.1.156" {
		t.Fatalf("unexpected Claude Code UA: %s", anthropic.ClaudeCodeUserAgent)
	}
	if anthropic.ClaudeCodeSessionID == "" {
		t.Fatal("Claude session id should be process-scoped and non-empty")
	}
}

func TestGeminiBrowserAuthURLMatchesClientFingerprint(t *testing.T) {
	got := gemini.BuildAuthURL("client", "http://127.0.0.1:34567/oauth2callback", "", "state")
	query := strings.TrimPrefix(got, gemini.DefaultAuthURL+"?")
	want := strings.Join([]string{
		"response_type=code",
		"client_id=client",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A34567%2Foauth2callback",
		"access_type=offline",
		"scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcloud-platform%20https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.email%20https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.profile",
		"state=state",
	}, "&")
	if query != want {
		t.Fatalf("query mismatch:\nwant %s\n got %s", want, query)
	}
	if strings.Contains(query, "prompt=") || strings.Contains(query, "code_challenge=") || strings.Contains(query, "+") {
		t.Fatalf("Gemini browser auth query has unexpected params/encoding: %s", query)
	}
}

func TestGeminiUserAgentFingerprint(t *testing.T) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	want := "GeminiCLI/" + gemini.GeminiCLIVersion + "/gemini-2.5-pro (" + runtime.GOOS + "; " + arch + "; cli)"
	if got := gemini.GeminiCLIUserAgent("gemini-2.5-pro"); got != want {
		t.Fatalf("unexpected Gemini CLI UA:\nwant %s\n got %s", want, got)
	}
}
