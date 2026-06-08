package relay

import (
	"os"
	"path/filepath"
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
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
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