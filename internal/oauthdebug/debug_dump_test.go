package oauthdebug

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactValueRedactsTokenStructsOnly(t *testing.T) {
	type tokenResult struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		APIKey       string `json:"api_key"`
		AccountID    string `json:"account_id"`
		UserAgent    string `json:"user_agent"`
		ClientSecret string `json:"client_secret"`
	}

	redacted, ok := RedactValue(tokenResult{
		AccessToken:  "access",
		RefreshToken: "refresh",
		IDToken:      "id",
		APIKey:       "key",
		AccountID:    "acct_123",
		UserAgent:    "codex_cli_rs/0.138.0",
		ClientSecret: "debug-visible",
	}).(map[string]interface{})
	if !ok {
		t.Fatalf("RedactValue returned %T, want map", redacted)
	}
	for _, key := range []string{"access_token", "refresh_token", "id_token", "api_key"} {
		if redacted[key] != "[redacted]" {
			t.Fatalf("%s = %v, want redacted", key, redacted[key])
		}
	}
	if redacted["account_id"] != "acct_123" {
		t.Fatalf("account_id = %v", redacted["account_id"])
	}
	if redacted["user_agent"] != "codex_cli_rs/0.138.0" {
		t.Fatalf("user_agent = %v", redacted["user_agent"])
	}
	if redacted["client_secret"] != "debug-visible" {
		t.Fatalf("client_secret = %v", redacted["client_secret"])
	}
}

func TestNewHTTPDebugKeepsUsefulHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/token?project_id=p1&api_key=secret", strings.NewReader("access_token=a&client_secret=visible"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer access")
	req.Header.Set("User-Agent", "official-client/1.0")
	req.Header.Set("originator", "codex_cli_rs")

	debugInfo := NewHTTPDebug(req, []byte("access_token=a&client_secret=visible"))
	if strings.Contains(debugInfo.Request.URL, "secret") {
		t.Fatalf("URL leaked api key: %s", debugInfo.Request.URL)
	}
	if debugInfo.Request.Headers["Authorization"] != "[redacted]" {
		t.Fatalf("Authorization = %v", debugInfo.Request.Headers["Authorization"])
	}
	if debugInfo.Request.Headers["User-Agent"] != "official-client/1.0" {
		t.Fatalf("User-Agent = %v", debugInfo.Request.Headers["User-Agent"])
	}
	if debugInfo.Request.Headers["Originator"] != "codex_cli_rs" {
		t.Fatalf("Originator = %v", debugInfo.Request.Headers["Originator"])
	}
	if strings.Contains(debugInfo.Request.Body, "access_token=a") {
		t.Fatalf("body leaked access token: %s", debugInfo.Request.Body)
	}
	if !strings.Contains(debugInfo.Request.Body, "client_secret=visible") {
		t.Fatalf("body over-redacted client_secret: %s", debugInfo.Request.Body)
	}
}

func TestWriteCreatesOAuthDumpInDebugDumps(t *testing.T) {
	dir := t.TempDir()
	Configure(dir)
	t.Cleanup(func() { Configure("") })

	Write("codex", "exchange_code", map[string]interface{}{
		"account_id":    "acct_123",
		"refresh_token": "secret-refresh",
	}, nil, map[string]interface{}{
		"access_token": "secret-access",
		"account_id":   "acct_123",
	}, nil)

	dayDir := filepath.Join(dir, timeNowLocalDate())
	var dumpPath string
	if err := filepath.WalkDir(dayDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "oauth.json" {
			dumpPath = path
		}
		return nil
	}); err != nil {
		t.Fatalf("walk day dir: %v", err)
	}
	if dumpPath == "" {
		t.Fatal("oauth.json was not written")
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read oauth dump: %v", err)
	}
	if strings.Contains(string(raw), "secret-refresh") || strings.Contains(string(raw), "secret-access") {
		t.Fatalf("dump leaked token: %s", raw)
	}
	var dump Dump
	if err := json.Unmarshal(raw, &dump); err != nil {
		t.Fatalf("decode dump: %v", err)
	}
	if dump.Provider != "codex" || dump.Operation != "exchange_code" {
		t.Fatalf("dump provider/operation = %q/%q", dump.Provider, dump.Operation)
	}
	if dump.Metadata["account_id"] != "acct_123" {
		t.Fatalf("metadata account_id = %v", dump.Metadata["account_id"])
	}
}

func timeNowLocalDate() string {
	return strings.Split(time.Now().Local().Format("2006-01-02T15:04:05"), "T")[0]
}
