package openai_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

// Mirrors upstream codex_cli_rs (login/src/auth/manager.rs:900-915): refresh
// requests are JSON-encoded with Content-Type: application/json and carry the
// codex client fingerprint headers.
func TestCodexRefreshRequestUsesJSONClientFingerprint(t *testing.T) {
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
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid JSON body %q: %v", string(body), err)
	}
	wants := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "refresh-token",
		"client_id":     "codex-client",
		"client_secret": "codex-secret",
	}
	for k, v := range wants {
		if payload[k] != v {
			t.Fatalf("payload[%q] = %q, want %q", k, payload[k], v)
		}
	}
}
