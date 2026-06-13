package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateTarGz(t *testing.T) {
	dir := t.TempDir()
	// Create test files
	subDir := filepath.Join(dir, "2026-06-07")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(dir, "2026-06-07.tar.gz")
	if err := createTarGz(subDir, archivePath); err != nil {
		t.Fatalf("createTarGz() error = %v", err)
	}

	// Verify archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Fatal("archive was not created")
	}

	// Verify we can list the archive
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// createTarGz does not remove source dir; both archive and source dir remain
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (archive + source dir), got %d", len(entries))
	}
}

func TestCleanupOldArchives(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// Create archives with different ages
	oldArchive := "2026-06-01.tar.gz"
	newArchive := "2026-06-07.tar.gz"

	// Create old archive (will be removed)
	oldPath := filepath.Join(dir, oldArchive)
	if f, err := os.Create(oldPath); err == nil {
		f.Close()
		os.Chtimes(oldPath, now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))
	}

	// Create new archive (will be kept)
	newPath := filepath.Join(dir, newArchive)
	if f, err := os.Create(newPath); err == nil {
		f.Close()
		os.Chtimes(newPath, now.Add(-1*24*time.Hour), now.Add(-1*24*time.Hour))
	}

	// Test with maxEntries = 1 (should keep only 1)
	t.Setenv("UAPI_RELAY_DEBUG_DUMP_MAX_ENTRIES", "1")
	relayDebugDumpDir = dir
	cleanupOldArchives(dir)

	// Check: new archive should exist, old should be removed
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("new archive was removed unexpectedly")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old archive should have been removed")
	}
}

func TestRelayDebugDumpCurrentDayDir(t *testing.T) {
	relayDebugDumpDir = ""
	if got := relayDebugDumpCurrentDayDir(); got != "" {
		t.Errorf("relayDebugDumpCurrentDayDir() = %v, want empty", got)
	}

	relayDebugDumpDir = "/tmp/debug-dumps"
	expected := filepath.Join("/tmp/debug-dumps", time.Now().Local().Format("2006-01-02"))
	if got := relayDebugDumpCurrentDayDir(); got != expected {
		t.Errorf("relayDebugDumpCurrentDayDir() = %v, want %v", got, expected)
	}
}

func TestRotateAndCleanupRelayDebugDumpDir(t *testing.T) {
	dir := t.TempDir()
	relayDebugDumpDir = dir

	// Create yesterday's directory with content
	yesterday := time.Now().AddDate(0, 0, -1).Local().Format("2006-01-02")
	yesterdayDir := filepath.Join(dir, yesterday)
	if err := os.MkdirAll(yesterdayDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(yesterdayDir, "test.json"), []byte(`{"test":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Run rotation
	rotateAndCleanupRelayDebugDumpDir()

	// Verify archive was created
	archivePath := filepath.Join(dir, yesterday+".tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Errorf("archive was not created at %s", archivePath)
	}

	// Verify original directory was removed
	if _, err := os.Stat(yesterdayDir); !os.IsNotExist(err) {
		t.Error("yesterday's directory should have been removed after archiving")
	}
}

func TestRelayDebugDumpRequestBodyPreservesFieldsAndTruncatesContent(t *testing.T) {
	longContent := strings.Repeat("hello", 200)
	body := []byte(`{
		"model":"gpt-5.5",
		"input":[{"type":"message","content":"` + longContent + `","metadata":{"keep":true}}],
		"tools":[{"type":"function","name":"do_work"}],
		"access_token":"secret-token",
		"metadata":{"session_id":"session-1"}
	}`)

	dumped := relayDebugDumpRequestBody(body)
	if strings.Contains(string(dumped), longContent) {
		t.Fatalf("full content leaked into dump: %s", dumped)
	}
	if !strings.Contains(string(dumped), "[truncated") {
		t.Fatalf("dump did not mark truncated content: %s", dumped)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(dumped, &got); err != nil {
		t.Fatalf("dumped body is not JSON: %v\n%s", err, dumped)
	}
	if got["model"] != "gpt-5.5" {
		t.Fatalf("model field missing: %#v", got)
	}
	if got["access_token"] != "[redacted]" {
		t.Fatalf("access token was not redacted: %#v", got["access_token"])
	}
	input, _ := got["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("input array not preserved: %#v", got["input"])
	}
	msg, _ := input[0].(map[string]interface{})
	if msg["type"] != "message" || msg["content"] == "" {
		t.Fatalf("input fields not preserved: %#v", msg)
	}
}

func TestRelayDebugDumpRequestBodyTruncatesInvalidJSONRawBody(t *testing.T) {
	body := []byte(strings.Repeat("x", relayDebugRequestRawLimit+64))
	dumped := relayDebugDumpRequestBody(body)
	if len(dumped) >= len(body) {
		t.Fatalf("raw body was not truncated: got %d want less than %d", len(dumped), len(body))
	}
	if !strings.Contains(string(dumped), "[truncated 64 bytes]") {
		t.Fatalf("raw truncation marker missing: %s", dumped[len(dumped)-80:])
	}
}

func TestRelayDebugDumpRequestBodyFullModeKeepsBody(t *testing.T) {
	t.Setenv(relayDebugDumpBodyModeEnv, "full")
	body := []byte(`{"content":"` + strings.Repeat("x", relayDebugRequestStringLimit+10) + `","access_token":"secret"}`)
	dumped := relayDebugDumpRequestBody(body)
	if string(dumped) != string(body) {
		t.Fatalf("full mode changed body:\n got %s\nwant %s", dumped, body)
	}
}
