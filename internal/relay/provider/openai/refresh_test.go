package openai_test

import (
	"io"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

func TestCodexRefreshRequestUsesFormEncodedClientFingerprint(t *testing.T) {
	req, err := openai.NewRefreshTokenRequest("https://auth.openai.com/oauth/token", "refresh-token", "codex-client", "codex-secret")
	if err != nil {
		t.Fatalf("NewRefreshTokenRequest: %v", err)
	}
	defer req.Body.Close()

	if got := req.Header.Get("originator"); got != openai.CodexOriginator {
		t.Fatalf("originator = %q, want %q", got, openai.CodexOriginator)
	}
	if got := req.Header.Get("User-Agent"); got != openai.CodexUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, openai.CodexUserAgent)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
		t.Fatalf("Content-Type = %q, want form encoded", got)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	form := string(body)
	for _, want := range []string{
		"grant_type=refresh_token",
		"refresh_token=refresh-token",
		"client_id=codex-client",
		"client_secret=codex-secret",
	} {
		if !strings.Contains(form, want) {
			t.Fatalf("form %q missing %q", form, want)
		}
	}
}
