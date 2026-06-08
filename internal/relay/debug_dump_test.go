package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupRelayDebugDumpDirWithLimitsRemovesOldEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 5, 7, 0, 0, 0, time.UTC)
	makeDumpEntry(t, dir, "old", now.Add(-2*time.Hour))
	makeDumpEntry(t, dir, "recent", now.Add(-30*time.Minute))

	removed, kept, err := cleanupRelayDebugDumpDirWithLimits(dir, time.Hour, 200, now)
	if err != nil {
		t.Fatalf("cleanupRelayDebugDumpDirWithLimits() error = %v", err)
	}
	if removed != 1 || kept != 1 {
		t.Fatalf("removed, kept = %d, %d; want 1, 1", removed, kept)
	}
	if pathExists(filepath.Join(dir, "old")) {
		t.Fatal("old entry was not removed")
	}
	if !pathExists(filepath.Join(dir, "recent")) {
		t.Fatal("recent entry was removed")
	}
}

func TestCleanupRelayDebugDumpDirWithLimitsKeepsNewestEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 5, 7, 0, 0, 0, time.UTC)
	makeDumpEntry(t, dir, "newest", now.Add(-1*time.Minute))
	makeDumpEntry(t, dir, "middle", now.Add(-2*time.Minute))
	makeDumpEntry(t, dir, "oldest", now.Add(-3*time.Minute))

	removed, kept, err := cleanupRelayDebugDumpDirWithLimits(dir, 0, 2, now)
	if err != nil {
		t.Fatalf("cleanupRelayDebugDumpDirWithLimits() error = %v", err)
	}
	if removed != 1 || kept != 2 {
		t.Fatalf("removed, kept = %d, %d; want 1, 2", removed, kept)
	}
	if !pathExists(filepath.Join(dir, "newest")) || !pathExists(filepath.Join(dir, "middle")) {
		t.Fatal("newer entries should be kept")
	}
	if pathExists(filepath.Join(dir, "oldest")) {
		t.Fatal("oldest entry was not removed")
	}
}

func TestRelayDebugDumpLocalTimestampsUseConfiguredLocalZone(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.FixedZone("CST", 8*60*60)
	t.Cleanup(func() {
		time.Local = previousLocal
	})

	now := time.Date(2026, 6, 8, 0, 1, 2, 3000000, time.UTC)
	name := relayDebugDumpEntryName(now, "trace")
	if !strings.HasPrefix(name, "20260608T080102.003000000+0800-trace") {
		t.Fatalf("relayDebugDumpEntryName() = %q, want Beijing +0800 timestamp", name)
	}
	if got := relayDebugDumpTimestamp(now); got != "2026-06-08T08:01:02.003+08:00" {
		t.Fatalf("relayDebugDumpTimestamp() = %q, want Beijing RFC3339 timestamp", got)
	}
}

func TestRelayDebugTraceTimingStateTracksStreamLatency(t *testing.T) {
	start := time.Date(2026, 6, 6, 16, 0, 0, 0, time.UTC)
	trace := &relayDebugTrace{
		ID:              "trace",
		Dir:             t.TempDir(),
		startedAt:       start,
		upstreamStarted: start.Add(2 * time.Millisecond),
		upstreamHeaders: start.Add(12 * time.Millisecond),
		streamBytes:     map[string]int{},
		streamTruncated: map[string]bool{},
		streamEvents:    map[string]int{},
		streamPayloads:  map[string]int{},
		streamLast:      map[string]map[string]interface{}{},
		streamFirstAt:   map[string]time.Time{},
		streamLastAt:    map[string]time.Time{},
		streamMaxGap:    map[string]time.Duration{},
	}

	trace.recordStreamChunkTimingLocked("stream.upstream.sse", start.Add(20*time.Millisecond))
	trace.recordStreamChunkTimingLocked("stream.upstream.sse", start.Add(170*time.Millisecond))
	trace.recordStreamChunkTimingLocked("stream.downstream.sse", start.Add(23*time.Millisecond))

	timing := trace.TimingState()
	if got := timing["upstream_request_to_headers_ms"]; got != int64(10) {
		t.Fatalf("upstream_request_to_headers_ms = %#v, want 10", got)
	}
	if got := timing["headers_to_upstream_first_ms"]; got != int64(8) {
		t.Fatalf("headers_to_upstream_first_ms = %#v, want 8", got)
	}
	if got := timing["first_upstream_to_first_downstream_ms"]; got != int64(3) {
		t.Fatalf("first_upstream_to_first_downstream_ms = %#v, want 3", got)
	}
	if got := timing["upstream_max_idle_ms"]; got != int64(150) {
		t.Fatalf("upstream_max_idle_ms = %#v, want 150", got)
	}
}

func makeDumpEntry(t *testing.T, dir, name string, modTime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("mkdir dump entry: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes dump entry: %v", err)
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
