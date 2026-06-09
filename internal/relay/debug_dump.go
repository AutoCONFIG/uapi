package relay

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
)

const (
	relayDebugDumpDirEnv            = "UAPI_RELAY_DEBUG_DUMP_DIR"
	relayDebugDumpMaxAgeEnv         = "UAPI_RELAY_DEBUG_DUMP_MAX_AGE"
	relayDebugDumpMaxEntriesEnv     = "UAPI_RELAY_DEBUG_DUMP_MAX_ENTRIES"
	relayDebugDumpDefaultMaxAge     = 7 * 24 * time.Hour  // 7 days
	relayDebugDumpDefaultMaxEntries = 7                   // keep 7 daily archives
	relayDebugStreamFileMaxSize     = 2 * 1024 * 1024
)

var (
	relayDebugDumpDir              = os.Getenv(relayDebugDumpDirEnv)
	relayDebugDumpProcessStartedAt = time.Now()
)

func init() {
	cleanupRelayDebugDumpDir()
	// Start daily rotation ticker
	go func() {
		// Calculate time until next midnight
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		duration := nextMidnight.Sub(now)

		timer := time.AfterFunc(duration, func() {
			rotateAndCleanupRelayDebugDumpDir()
			// Then run every 24 hours
			ticker := time.NewTicker(24 * time.Hour)
			for range ticker.C {
				rotateAndCleanupRelayDebugDumpDir()
			}
		})
		_ = timer // Let it run
	}()
}

func relayDebugDumpEnabled() bool {
	return relayDebugDumpDir != ""
}

// rotateAndCleanupRelayDebugDumpDir rotates yesterday's dump to .tar.gz and cleans up old archives
func rotateAndCleanupRelayDebugDumpDir() {
	if relayDebugDumpDir == "" {
		return
	}
	dir := filepath.Clean(relayDebugDumpDir)
	if dir == "." || dir == string(filepath.Separator) {
		logger.Warnf("relay.debug_dump", "skip unsafe dump dir", logger.F("dir", relayDebugDumpDir))
		return
	}

	yesterday := time.Now().AddDate(0, 0, -1).Local().Format("2006-01-02")
	dayDir := filepath.Join(dir, yesterday)
	archiveName := yesterday + ".tar.gz"
	archivePath := filepath.Join(dir, archiveName)

	// Check if day directory exists
	if _, err := os.Stat(dayDir); os.IsNotExist(err) {
		// No yesterday directory, nothing to rotate
		return
	}

	// Check if archive already exists
	if _, err := os.Stat(archivePath); err == nil {
		logger.Infof("relay.debug_dump", "archive already exists, skipping", logger.F("archive", archiveName))
	} else {
		// Create tar.gz archive
		if err := createTarGz(dayDir, archivePath); err != nil {
			logger.Warnf("relay.debug_dump", "create archive failed", logger.Err(err), logger.F("day_dir", dayDir), logger.F("archive", archivePath))
			return
		}
		logger.Infof("relay.debug_dump", "rotated daily dump to archive", logger.F("archive", archiveName))
	}

	// Remove the original day directory after successful archive
	if err := os.RemoveAll(dayDir); err != nil {
		logger.Warnf("relay.debug_dump", "remove day dir failed", logger.Err(err), logger.F("day_dir", dayDir))
	}

	// Clean up old archives
	cleanupOldArchives(dir)
}

func createTarGz(srcDir, destPath string) error {
	file, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzw := gzip.NewWriter(file)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Get relative path
		relPath, err := filepath.Rel(filepath.Dir(srcDir), path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, relPath)
		if err != nil {
			return err
		}
		header.Name = relPath
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}
		return nil
	})
}

func cleanupOldArchives(dir string) {
	maxEntries := relayDebugDumpMaxEntries()
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warnf("relay.debug_dump", "read dir for cleanup failed", logger.Err(err))
		return
	}

	// Collect .tar.gz archives
	var archives []relayDebugDumpEntry
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		archives = append(archives, relayDebugDumpEntry{name: name, modTime: info.ModTime()})
	}

	// Sort by modification time (newest first)
	sort.Slice(archives, func(i, j int) bool {
		return archives[i].modTime.After(archives[j].modTime)
	})

	// Remove old archives beyond maxEntries
	removed := 0
	if maxEntries > 0 && len(archives) > maxEntries {
		for _, arch := range archives[maxEntries:] {
			if removeRelayDebugDumpEntry(dir, arch.name) {
				removed++
			}
		}
	}

	if removed > 0 {
		logger.Infof("relay.debug_dump", "cleaned old archives", logger.F("removed", removed), logger.F("kept", len(archives)-removed))
	}
}

func cleanupRelayDebugDumpDir() {
	// Initial cleanup on startup: rotate and clean any stale day directories
	if relayDebugDumpDir == "" {
		return
	}
	dir := filepath.Clean(relayDebugDumpDir)
	if dir == "." || dir == string(filepath.Separator) {
		logger.Warnf("relay.debug_dump", "skip unsafe dump dir cleanup", logger.F("dir", relayDebugDumpDir))
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Warnf("relay.debug_dump", "create dump dir before cleanup failed", logger.Err(err), logger.F("dir", dir))
		return
	}

	// On startup: rotate yesterday if not already done, clean old archives
	rotateAndCleanupRelayDebugDumpDir()

	logger.Infof("relay.debug_dump", "debug dump initialized",
		logger.F("dir", relayDebugDumpDir),
		logger.F("max_archives", relayDebugDumpMaxEntries()),
	)
}

func relayDebugDumpMaxEntries() int {
	raw := os.Getenv(relayDebugDumpMaxEntriesEnv)
	if raw == "" {
		return relayDebugDumpDefaultMaxEntries
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		logger.Warnf("relay.debug_dump", "invalid max entries, using default", logger.F("env", relayDebugDumpMaxEntriesEnv), logger.F("value", raw))
		return relayDebugDumpDefaultMaxEntries
	}
	return n
}

type relayDebugDumpEntry struct {
	name    string
	modTime time.Time
}

func removeRelayDebugDumpEntry(dir, name string) bool {
	if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
		logger.Warnf("relay.debug_dump", "remove old dump entry failed", logger.Err(err), logger.F("dir", dir), logger.F("entry", name))
		return false
	}
	return true
}

type relayDebugDumpSummary struct {
	Timestamp        string                                `json:"timestamp"`
	RelayRequestID   string                                `json:"relay_request_id"`
	ProcessID        int                                   `json:"process_id"`
	ProcessStartedAt string                                `json:"process_started_at"`
	TokenID          string                                `json:"token_id,omitempty"`
	AccountID        string                                `json:"account_id,omitempty"`
	ChannelID        string                                `json:"channel_id,omitempty"`
	ChannelName      string                                `json:"channel_name,omitempty"`
	ChannelType      string                                `json:"channel_type,omitempty"`
	APIFormat        string                                `json:"api_format,omitempty"`
	GatewayRequest   string                                `json:"gateway_request,omitempty"`
	ClientFormat     provider.Format                       `json:"client_format"`
	UpstreamFormat   provider.Format                       `json:"upstream_format"`
	RequestType      string                                `json:"request_type"`
	Model            string                                `json:"model"`
	RoutedModel      string                                `json:"routed_model"`
	Stream           bool                                  `json:"stream"`
	OriginalBytes    int                                   `json:"original_bytes"`
	ConvertedBytes   int                                   `json:"converted_bytes"`
	CachePassthrough upstreamconfig.CachePassthroughPolicy `json:"cache_passthrough"`
	Original         relayDebugRequestStats                `json:"original"`
	Converted        relayDebugRequestStats                `json:"converted"`
	RouteAttempts    []map[string]interface{}              `json:"route_attempts,omitempty"`
	FallbackReasons  []string                              `json:"fallback_reasons,omitempty"`
}

type relayDebugRequestStats struct {
	ToolsLen          int                     `json:"tools_len,omitempty"`
	ToolsBytes        int                     `json:"tools_bytes,omitempty"`
	ToolsHash         string                  `json:"tools_hash,omitempty"`
	MessagesLen       int                     `json:"messages_len,omitempty"`
	MessagePrefixes   []relayDebugPrefix      `json:"message_prefixes,omitempty"`
	InputLen          int                     `json:"input_len,omitempty"`
	PromptCacheKey    json.RawMessage         `json:"prompt_cache_key,omitempty"`
	PromptCacheSource string                  `json:"prompt_cache_source,omitempty"`
	CachedContent     string                  `json:"cached_content,omitempty"`
	CacheControlCount int                     `json:"cache_control_count,omitempty"`
	CacheMarkers      []relayDebugCacheMarker `json:"cache_markers,omitempty"`
	ToolChoice        json.RawMessage         `json:"tool_choice,omitempty"`
	ParallelToolCalls json.RawMessage         `json:"parallel_tool_calls,omitempty"`
	HasThinking       bool                    `json:"has_thinking,omitempty"`
}

type relayDebugCacheMarker struct {
	Path       string `json:"path"`
	Role       string `json:"role,omitempty"`
	Trailing   bool   `json:"trailing,omitempty"`
	PrefixHash string `json:"prefix_hash,omitempty"`
}

type relayDebugPrefix struct {
	Count int    `json:"count"`
	Hash  string `json:"hash"`
}

type relayDebugTrace struct {
	ID              string
	Dir             string
	startedAt       time.Time
	mu              sync.Mutex
	upstreamStarted time.Time
	upstreamHeaders time.Time
	streamBytes     map[string]int
	streamTruncated map[string]bool
	streamEvents    map[string]int
	streamPayloads  map[string]int
	streamLast      map[string]map[string]interface{}
	streamFirstAt   map[string]time.Time
	streamLastAt    map[string]time.Time
	streamMaxGap    map[string]time.Duration
	// Null chunk dedup: track consecutive null chunks per stream name.
	streamConsecutiveNulls map[string]int
	streamLastWasNull      map[string]bool
	routingInfo            map[string]interface{}
}

// SetRoutingInfo stores route attempts and fallback paths for the debug dump.
func (t *relayDebugTrace) SetRoutingInfo(info map[string]interface{}) {
	if t == nil {
		return
	}
	t.routingInfo = info
}

// WriteRoutingInfo persists routing decisions to the debug dump directory.
func (t *relayDebugTrace) WriteRoutingInfo() {
	if t == nil || t.Dir == "" {
		return
	}
	info := t.routingInfo
	if info == nil {
		info = map[string]interface{}{}
	}
	if raw, err := json.MarshalIndent(info, "", "  "); err == nil {
		writeRelayDebugFile(t.Dir, "routing.json", raw)
	}
}

func relayDebugDumpTimestamp(t time.Time) string {
	return t.Local().Format(time.RFC3339Nano)
}

func relayDebugDumpCurrentDayDir() string {
	if relayDebugDumpDir == "" {
		return ""
	}
	// Use local time date as subdirectory name
	day := time.Now().Local().Format("2006-01-02")
	return filepath.Join(relayDebugDumpDir, day)
}

func relayDebugDumpEntryName(t time.Time, traceID string) string {
	return t.Local().Format("20060102T150405.000000000-0700") + "-" + traceID
}

func startRelayRequestDebugDump(original, converted []byte, token db.Token, ch *db.Channel, acc *db.Account, claims *internalauth.Claims, clientFormat, upstreamFormat provider.Format, requestType relayRequestType, model, routedModel string, stream bool) *relayDebugTrace {
	dayDir := relayDebugDumpCurrentDayDir()
	if dayDir == "" {
		return nil
	}
	traceID := uuid.NewString()
	now := time.Now()
	name := relayDebugDumpEntryName(now, traceID)
	outDir := filepath.Join(dayDir, name)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logger.Warnf("relay.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", outDir))
		return nil
	}

	summary := relayDebugDumpSummary{
		Timestamp:        relayDebugDumpTimestamp(now),
		RelayRequestID:   traceID,
		ProcessID:        os.Getpid(),
		ProcessStartedAt: relayDebugDumpTimestamp(relayDebugDumpProcessStartedAt),
		TokenID:          token.ID.String(),
		ClientFormat:     clientFormat,
		UpstreamFormat:   upstreamFormat,
		RequestType:      string(requestType),
		Model:            model,
		RoutedModel:      routedModel,
		Stream:           stream,
		OriginalBytes:    len(original),
		ConvertedBytes:   len(converted),
		CachePassthrough: upstreamconfig.CachePassthroughPolicyForChannel(ch, upstreamFormat),
		Original:         relayDebugRequestStatsFromBody(original),
		Converted:        relayDebugRequestStatsFromBody(converted),
	}
	if ch != nil {
		summary.ChannelID = ch.ID.String()
		summary.ChannelName = ch.Name
		summary.ChannelType = ch.Type
		summary.APIFormat = ch.APIFormat
	}
	if acc != nil {
		summary.AccountID = acc.ID.String()
	}
	if claims != nil {
		summary.GatewayRequest = claims.RequestID
	}

	writeRelayDebugFile(outDir, "request.original.json", original)
	writeRelayDebugFile(outDir, "request.converted.json", converted)
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		writeRelayDebugFile(outDir, "summary.json", raw)
	}
	trace := &relayDebugTrace{
		ID:                    traceID,
		Dir:                   outDir,
		startedAt:             now,
		streamBytes:           map[string]int{},
		streamTruncated:       map[string]bool{},
		streamEvents:          map[string]int{},
		streamPayloads:        map[string]int{},
		streamLast:            map[string]map[string]interface{}{},
		streamFirstAt:         map[string]time.Time{},
		streamLastAt:          map[string]time.Time{},
		streamMaxGap:          map[string]time.Duration{},
		streamConsecutiveNulls: map[string]int{},
		streamLastWasNull:     map[string]bool{},
	}
	trace.Event("request_dump_written",
		logger.F("client_format", string(clientFormat)),
		logger.F("upstream_format", string(upstreamFormat)),
		logger.F("request_type", string(requestType)),
		logger.F("model", model),
		logger.F("routed_model", routedModel),
		logger.F("stream", stream),
		logger.F("original_bytes", len(original)),
		logger.F("converted_bytes", len(converted)),
		logger.F("original_tools_len", summary.Original.ToolsLen),
		logger.F("converted_tools_len", summary.Converted.ToolsLen),
		logger.F("original_tools_hash", summary.Original.ToolsHash),
		logger.F("converted_tools_hash", summary.Converted.ToolsHash),
		logger.F("original_tool_choice", relayDebugRawString(summary.Original.ToolChoice)),
		logger.F("converted_tool_choice", relayDebugRawString(summary.Converted.ToolChoice)),
		logger.F("original_parallel_tool_calls", relayDebugRawString(summary.Original.ParallelToolCalls)),
		logger.F("converted_parallel_tool_calls", relayDebugRawString(summary.Converted.ParallelToolCalls)),
		logger.F("original_prompt_cache_key", relayDebugRawString(summary.Original.PromptCacheKey)),
		logger.F("converted_prompt_cache_key", relayDebugRawString(summary.Converted.PromptCacheKey)),
		logger.F("original_prompt_cache_source", summary.Original.PromptCacheSource),
		logger.F("converted_prompt_cache_source", summary.Converted.PromptCacheSource),
		logger.F("original_cached_content", summary.Original.CachedContent),
		logger.F("converted_cached_content", summary.Converted.CachedContent),
		logger.F("original_cache_control_count", summary.Original.CacheControlCount),
		logger.F("converted_cache_control_count", summary.Converted.CacheControlCount),
		logger.F("cache_passthrough_enabled", summary.CachePassthrough.Enabled),
		logger.F("cache_passthrough_prompt_cache_key", summary.CachePassthrough.PromptCacheKey),
		logger.F("cache_passthrough_cache_control", summary.CachePassthrough.CacheControl),
		logger.F("cache_passthrough_cached_content", summary.CachePassthrough.CachedContent),
	)
	logger.Debugf("relay.debug_dump", "request dump written",
		logger.F("relay_request_id", traceID),
		logger.F("dump_dir", outDir),
		logger.F("client_format", string(clientFormat)),
		logger.F("upstream_format", string(upstreamFormat)),
		logger.F("original_bytes", len(original)),
		logger.F("converted_bytes", len(converted)),
	)
	return trace
}

func relayDebugRequestStatsFromBody(body []byte) relayDebugRequestStats {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return relayDebugRequestStats{}
	}
	stats := relayDebugRequestStats{}
	if raw := root["tools"]; len(raw) > 0 {
		var tools []json.RawMessage
		if err := json.Unmarshal(raw, &tools); err == nil {
			stats.ToolsLen = len(tools)
			stats.ToolsBytes = len(raw)
			stats.ToolsHash = relayDebugCanonicalHash(raw)
		}
	}
	if raw := root["messages"]; len(raw) > 0 {
		var messages []json.RawMessage
		if err := json.Unmarshal(raw, &messages); err == nil {
			stats.MessagesLen = len(messages)
			stats.MessagePrefixes = relayDebugMessagePrefixes(root, len(messages))
		}
	}
	if raw := root["input"]; len(raw) > 0 {
		var input []json.RawMessage
		if err := json.Unmarshal(raw, &input); err == nil {
			stats.InputLen = len(input)
		}
	}
	if raw := root["tool_choice"]; len(raw) > 0 {
		stats.ToolChoice = append(json.RawMessage(nil), raw...)
	}
	if raw := root["parallel_tool_calls"]; len(raw) > 0 {
		stats.ParallelToolCalls = append(json.RawMessage(nil), raw...)
	}
	if raw := root["prompt_cache_key"]; len(raw) > 0 {
		stats.PromptCacheKey = append(json.RawMessage(nil), raw...)
		stats.PromptCacheSource = "prompt_cache_key"
	} else if source := relayDebugPromptCacheSource(root); source != "" {
		stats.PromptCacheSource = source
	}
	if raw := root["cachedContent"]; len(raw) > 0 {
		stats.CachedContent = relayDebugRawJSONStr(raw)
	}
	var anyRoot interface{}
	if err := relayDebugDecodeJSONUseNumber(body, &anyRoot); err == nil {
		stats.CacheControlCount = relayDebugCountJSONKey(anyRoot, "cache_control")
		stats.CacheMarkers = relayDebugCacheMarkers(root)
	}
	if raw := root["thinking"]; len(raw) > 0 && string(raw) != "null" {
		stats.HasThinking = true
	}
	return stats
}

func relayDebugCacheMarkers(root map[string]json.RawMessage) []relayDebugCacheMarker {
	var markers []relayDebugCacheMarker
	if raw := root["messages"]; len(raw) > 0 {
		var messages []map[string]interface{}
		if err := json.Unmarshal(raw, &messages); err == nil {
			for i, msg := range messages {
				role, _ := msg["role"].(string)
				trailing := i == len(messages)-1
				if _, ok := msg["cache_control"]; ok {
					markers = append(markers, relayDebugCacheMarker{
						Path:       "messages." + strconv.Itoa(i),
						Role:       role,
						Trailing:   trailing,
						PrefixHash: relayDebugPrefixHash(root, "messages", i, -1),
					})
				}
				if content, ok := msg["content"].([]interface{}); ok {
					for j, part := range content {
						partMap, ok := part.(map[string]interface{})
						if !ok {
							continue
						}
						if _, ok := partMap["cache_control"]; ok {
							markers = append(markers, relayDebugCacheMarker{
								Path:       "messages." + strconv.Itoa(i) + ".content." + strconv.Itoa(j),
								Role:       role,
								Trailing:   trailing,
								PrefixHash: relayDebugPrefixHash(root, "messages", i, j),
							})
						}
					}
				}
			}
		}
	}
	if raw := root["input"]; len(raw) > 0 {
		var input []map[string]interface{}
		if err := json.Unmarshal(raw, &input); err == nil {
			for i, item := range input {
				if _, ok := item["cache_control"]; ok {
					markers = append(markers, relayDebugCacheMarker{
						Path:       "input." + strconv.Itoa(i),
						Trailing:   i == len(input)-1,
						PrefixHash: relayDebugPrefixHash(root, "input", i, -1),
					})
				}
			}
		}
	}
	if raw := root["tools"]; len(raw) > 0 {
		var tools []map[string]interface{}
		if err := json.Unmarshal(raw, &tools); err == nil {
			for i, tool := range tools {
				if _, ok := tool["cache_control"]; ok {
					markers = append(markers, relayDebugCacheMarker{
						Path:       "tools." + strconv.Itoa(i),
						PrefixHash: relayDebugPrefixHash(root, "tools", i, -1),
					})
				}
			}
		}
	}
	return markers
}

func relayDebugCanonicalHash(raw json.RawMessage) string {
	var value interface{}
	if err := relayDebugDecodeJSONUseNumber(raw, &value); err != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:8])
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:8])
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

func relayDebugMessagePrefixes(root map[string]json.RawMessage, messagesLen int) []relayDebugPrefix {
	if messagesLen <= 0 {
		return nil
	}
	counts := []int{1, 2, 4, 8, 16, 32, 64}
	prefixes := make([]relayDebugPrefix, 0, len(counts))
	for _, count := range counts {
		if count > messagesLen {
			break
		}
		prefixes = append(prefixes, relayDebugPrefix{
			Count: count,
			Hash:  relayDebugPrefixHash(root, "messages", count-1, -1),
		})
	}
	return prefixes
}

func relayDebugPromptCacheSource(root map[string]json.RawMessage) string {
	for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId"} {
		if relayDebugRawJSONStr(root[key]) != "" {
			return key
		}
	}
	if raw := root["metadata"]; len(raw) > 0 {
		if source := relayDebugPromptCacheSourceFromObject(raw, "metadata"); source != "" {
			return source
		}
	}
	if raw := root["client_metadata"]; len(raw) > 0 {
		if source := relayDebugPromptCacheSourceFromObject(raw, "client_metadata"); source != "" {
			return source
		}
	}
	return ""
}

func relayDebugPromptCacheSourceFromObject(raw json.RawMessage, prefix string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, key := range []string{"prompt_cache_key", "session_id", "sessionId", "conversation_id", "conversationId"} {
		if relayDebugRawJSONStr(obj[key]) != "" {
			return prefix + "." + key
		}
	}
	for _, key := range []string{"user_id", "user"} {
		if source := relayDebugPromptCacheSourceFromNestedString(obj[key], prefix+"."+key); source != "" {
			return source
		}
	}
	return ""
}

func relayDebugPromptCacheSourceFromNestedString(raw json.RawMessage, prefix string) string {
	if len(raw) == 0 {
		return ""
	}
	if source := relayDebugPromptCacheSourceFromObject(raw, prefix); source != "" {
		return source
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || strings.TrimSpace(text) == "" || !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return ""
	}
	return relayDebugPromptCacheSourceFromObject(json.RawMessage(strings.TrimSpace(text)), prefix)
}

func relayDebugPrefixHash(root map[string]json.RawMessage, field string, itemIdx, partIdx int) string {
	clone := map[string]json.RawMessage{}
	for key, value := range root {
		clone[key] = value
	}
	raw := root[field]
	if len(raw) == 0 {
		return ""
	}
	var items []interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	if itemIdx >= 0 && itemIdx < len(items) {
		items = items[:itemIdx+1]
		if partIdx >= 0 {
			if msg, ok := items[itemIdx].(map[string]interface{}); ok {
				if content, ok := msg["content"].([]interface{}); ok && partIdx < len(content) {
					msg["content"] = content[:partIdx+1]
				}
			}
		}
	}
	prefixRaw, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	clone[field] = prefixRaw
	rawPrefix, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(rawPrefix)
	return hex.EncodeToString(sum[:8])
}

func relayDebugCountJSONKey(value interface{}, key string) int {
	switch v := value.(type) {
	case map[string]interface{}:
		count := 0
		for k, child := range v {
			if k == key {
				count++
			}
			count += relayDebugCountJSONKey(child, key)
		}
		return count
	case []interface{}:
		count := 0
		for _, child := range v {
			count += relayDebugCountJSONKey(child, key)
		}
		return count
	default:
		return 0
	}
}

func relayDebugDecodeJSONUseNumber(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

func relayDebugRawJSONStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func relayDebugRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func writeRelayDebugFile(dir, name string, body []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
		logger.Warnf("relay.debug_dump", "write dump file failed", logger.Err(err), logger.F("file", name))
	}
}

func (t *relayDebugTrace) Event(name string, fields ...logger.Field) {
	if t == nil {
		return
	}
	t.recordLifecycleEvent(name)
	record := map[string]interface{}{
		"timestamp":        relayDebugDumpTimestamp(time.Now()),
		"relay_request_id": t.ID,
		"event":            name,
	}
	for _, field := range fields {
		if field.Key == "" {
			continue
		}
		record[field.Key] = field.Value
	}
	if relayDebugEventNeedsTiming(name) {
		record["stream_timing"] = t.TimingState()
	}
	raw, err := json.Marshal(record)
	if err != nil {
		logger.Warnf("relay.debug_dump", "marshal event failed", logger.Err(err), logger.F("relay_request_id", t.ID))
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeJSONLRawLocked("events.jsonl", raw)
}

func (t *relayDebugTrace) recordLifecycleEvent(name string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	switch name {
	case "upstream_request_started":
		if t.upstreamStarted.IsZero() {
			t.upstreamStarted = now
		}
	case "upstream_headers_received":
		if t.upstreamHeaders.IsZero() {
			t.upstreamHeaders = now
		}
	}
}

func relayDebugEventNeedsTiming(name string) bool {
	return strings.HasPrefix(name, "stream_result_") ||
		name == "stream_forward_finished" ||
		name == "scanner_eof" ||
		name == "scanner_error" ||
		name == "scanner_closed_by_downstream" ||
		name == "scanner_benign_close_after_terminal" ||
		name == "scanner_benign_close_before_terminal" ||
		name == "stream_eof_without_terminal"
}

func (t *relayDebugTrace) StreamChunk(name string, body []byte) {
	if t == nil || len(body) == 0 || name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordStreamChunkTimingLocked(name, time.Now())

	// Null chunk dedup: detect consecutive empty/null chunks and skip writing
	// them individually. Only the first and last null chunk are written; a
	// summary line records how many were skipped. This reduces dump files from
	// megabytes to kilobytes for requests where upstream sends thousands of
	// empty heartbeat chunks during reasoning.
	isNull := isDebugNullChunk(body)
	if isNull {
		t.streamConsecutiveNulls[name]++
		t.streamLastWasNull[name] = true
		// Always write the first null chunk so the stream file isn't empty.
		if t.streamConsecutiveNulls[name] == 1 {
			t.writeStreamChunkLocked(name, body)
		}
		return
	}
	// Non-null chunk: flush any pending null streak with a summary comment.
	if t.streamLastWasNull[name] {
		skipped := t.streamConsecutiveNulls[name]
		if skipped > 1 {
			summary := []byte(fmt.Sprintf("\n# null_chunk_dedup: skipped %d consecutive empty chunks\n\n", skipped))
			t.writeStreamChunkLocked(name, summary)
		}
		t.streamConsecutiveNulls[name] = 0
		t.streamLastWasNull[name] = false
	}
	t.writeStreamChunkLocked(name, body)
}

func (t *relayDebugTrace) writeStreamChunkLocked(name string, body []byte) {

	written := t.streamBytes[name]
	if written >= relayDebugStreamFileMaxSize {
		t.noteStreamTruncatedLocked(name, written)
		return
	}
	remaining := relayDebugStreamFileMaxSize - written
	chunk := body
	truncated := false
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
		truncated = true
	}

	f, err := os.OpenFile(filepath.Join(t.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warnf("relay.debug_dump", "open stream dump file failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
		return
	}
	if _, err := f.Write(chunk); err != nil {
		logger.Warnf("relay.debug_dump", "write stream dump failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
		_ = f.Close()
		return
	}
	t.streamBytes[name] += len(chunk)
	if truncated {
		_, _ = f.Write([]byte("\n\n# UAPI debug stream truncated\n"))
		t.noteStreamTruncatedLocked(name, t.streamBytes[name])
	}
	if err := f.Close(); err != nil {
		logger.Warnf("relay.debug_dump", "close stream dump failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
	}
	t.writeStreamEventSummaryLocked(name, body, len(chunk), truncated)
}

func (t *relayDebugTrace) recordStreamChunkTimingLocked(name string, now time.Time) {
	if t.streamFirstAt[name].IsZero() {
		t.streamFirstAt[name] = now
	}
	if last := t.streamLastAt[name]; !last.IsZero() {
		if gap := now.Sub(last); gap > t.streamMaxGap[name] {
			t.streamMaxGap[name] = gap
		}
	}
	t.streamLastAt[name] = now
}

func (t *relayDebugTrace) StreamState(name string) map[string]interface{} {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := map[string]interface{}{
		"file":          name,
		"events":        t.streamEvents[name],
		"data_payloads": t.streamPayloads[name],
		"bytes":         t.streamBytes[name],
		"truncated":     t.streamTruncated[name],
	}
	if !t.startedAt.IsZero() {
		if first := t.streamFirstAt[name]; !first.IsZero() {
			state["first_ms"] = first.Sub(t.startedAt).Milliseconds()
		}
		if last := t.streamLastAt[name]; !last.IsZero() {
			state["last_ms"] = last.Sub(t.startedAt).Milliseconds()
		}
	}
	if gap := t.streamMaxGap[name]; gap > 0 {
		state["max_gap_ms"] = gap.Milliseconds()
	}
	if last := t.streamLast[name]; last != nil {
		state["last"] = last
	}
	return state
}

func (t *relayDebugTrace) StreamStates(names ...string) []map[string]interface{} {
	if t == nil {
		return nil
	}
	states := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		states = append(states, t.StreamState(name))
	}
	return states
}

func (t *relayDebugTrace) TimingState() map[string]interface{} {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]interface{}{}
	if !t.startedAt.IsZero() {
		out["request_started_at"] = relayDebugDumpTimestamp(t.startedAt)
	}
	if !t.upstreamStarted.IsZero() {
		out["upstream_request_started_ms"] = t.upstreamStarted.Sub(t.startedAt).Milliseconds()
	}
	if !t.upstreamHeaders.IsZero() {
		out["upstream_headers_ms"] = t.upstreamHeaders.Sub(t.startedAt).Milliseconds()
		if !t.upstreamStarted.IsZero() {
			out["upstream_request_to_headers_ms"] = t.upstreamHeaders.Sub(t.upstreamStarted).Milliseconds()
		}
	}
	addStream := func(prefix, name string) {
		first := t.streamFirstAt[name]
		last := t.streamLastAt[name]
		if !first.IsZero() {
			out[prefix+"_first_ms"] = first.Sub(t.startedAt).Milliseconds()
			if !t.upstreamHeaders.IsZero() {
				out["headers_to_"+prefix+"_first_ms"] = first.Sub(t.upstreamHeaders).Milliseconds()
			}
		}
		if !last.IsZero() {
			out[prefix+"_last_ms"] = last.Sub(t.startedAt).Milliseconds()
		}
		if gap := t.streamMaxGap[name]; gap > 0 {
			out[prefix+"_max_idle_ms"] = gap.Milliseconds()
		}
	}
	addStream("upstream", "stream.upstream.sse")
	addStream("normalized", "stream.normalized.sse")
	addStream("downstream", "stream.downstream.sse")
	if upstreamFirst := t.streamFirstAt["stream.upstream.sse"]; !upstreamFirst.IsZero() {
		if downstreamFirst := t.streamFirstAt["stream.downstream.sse"]; !downstreamFirst.IsZero() {
			out["first_upstream_to_first_downstream_ms"] = downstreamFirst.Sub(upstreamFirst).Milliseconds()
		}
	}
	if upstreamLast := t.streamLastAt["stream.upstream.sse"]; !upstreamLast.IsZero() {
		if downstreamLast := t.streamLastAt["stream.downstream.sse"]; !downstreamLast.IsZero() {
			out["last_upstream_to_last_downstream_ms"] = downstreamLast.Sub(upstreamLast).Milliseconds()
		}
	}
	return out
}

func (t *relayDebugTrace) noteStreamTruncatedLocked(name string, written int) {
	if t.streamTruncated[name] {
		return
	}
	t.streamTruncated[name] = true
	record := map[string]interface{}{
		"timestamp":        relayDebugDumpTimestamp(time.Now()),
		"relay_request_id": t.ID,
		"event":            "stream_dump_truncated",
		"file":             name,
		"limit":            relayDebugStreamFileMaxSize,
		"bytes":            written,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		logger.Warnf("relay.debug_dump", "marshal truncation event failed", logger.Err(err), logger.F("relay_request_id", t.ID))
		return
	}
	f, err := os.OpenFile(filepath.Join(t.Dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warnf("relay.debug_dump", "open events file failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir))
		return
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		logger.Warnf("relay.debug_dump", "write truncation event failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir))
	}
}

func (t *relayDebugTrace) writeStreamEventSummaryLocked(name string, body []byte, written int, truncated bool) {
	t.streamEvents[name]++
	record := relayDebugSSEEventSummary(body)
	if n, ok := record["data_payloads"].(int); ok {
		t.streamPayloads[name] += n
	}
	record["timestamp"] = relayDebugDumpTimestamp(time.Now())
	record["relay_request_id"] = t.ID
	record["event"] = "stream_chunk"
	record["file"] = name
	record["index"] = t.streamEvents[name]
	record["bytes"] = len(body)
	record["written_bytes"] = written
	record["truncated_write"] = truncated
	raw, err := json.Marshal(record)
	if err != nil {
		logger.Warnf("relay.debug_dump", "marshal stream event failed", logger.Err(err), logger.F("relay_request_id", t.ID))
		return
	}
	t.streamLast[name] = relayDebugCopyMap(record)
	t.writeJSONLRawLocked("stream.events.jsonl", raw)
}

func relayDebugCopyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (t *relayDebugTrace) writeJSONLRawLocked(name string, raw []byte) {
	f, err := os.OpenFile(filepath.Join(t.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warnf("relay.debug_dump", "open jsonl file failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
		return
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		logger.Warnf("relay.debug_dump", "write jsonl event failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
	}
}

func relayDebugSSEEventSummary(event []byte) map[string]interface{} {
	record := map[string]interface{}{}
	if line := relayDebugSSEEventLine(event, "event:"); line != "" {
		record["sse_event"] = line
	}
	payloads := relayDebugSSEDataPayloads(event)
	record["data_payloads"] = len(payloads)
	if len(payloads) == 0 {
		record["preview"] = relayDebugPreview(event, 240)
		return record
	}
	payload := []byte(payloads[len(payloads)-1])
	if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		record["data_done"] = true
		record["preview"] = "[DONE]"
		return record
	}
	var root map[string]interface{}
	if err := json.Unmarshal(payload, &root); err == nil {
		copyIfPresent(record, root, "type", "response_type")
		copyIfPresent(record, root, "sequence_number", "sequence_number")
		copyIfPresent(record, root, "output_index", "output_index")
		copyIfPresent(record, root, "content_index", "content_index")
		copyIfPresent(record, root, "item_id", "item_id")
		if item, ok := root["item"].(map[string]interface{}); ok {
			copyIfPresent(record, item, "type", "item_type")
			copyIfPresent(record, item, "status", "item_status")
			copyIfPresent(record, item, "phase", "item_phase")
			copyIfPresent(record, item, "name", "function_name")
		}
		if response, ok := root["response"].(map[string]interface{}); ok {
			copyIfPresent(record, response, "status", "response_status")
			copyIfPresent(record, response, "id", "response_id")
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				relayDebugAddUsageCacheTokens(record, usage)
			}
			if usageMetadata, ok := response["usageMetadata"].(map[string]interface{}); ok {
				relayDebugAddUsageCacheTokens(record, usageMetadata)
			}
		}
		if usage, ok := root["usage"].(map[string]interface{}); ok {
			relayDebugAddUsageCacheTokens(record, usage)
		}
		if usageMetadata, ok := root["usageMetadata"].(map[string]interface{}); ok {
			relayDebugAddUsageCacheTokens(record, usageMetadata)
		}
		if delta, ok := root["delta"].(string); ok && delta != "" {
			record["delta_preview"] = relayDebugPreview([]byte(delta), 160)
		}
		if text, ok := root["text"].(string); ok && text != "" {
			record["text_preview"] = relayDebugPreview([]byte(text), 160)
		}
	}
	record["preview"] = relayDebugPreview(payload, 240)
	return record
}

func relayDebugAddUsageCacheTokens(record map[string]interface{}, usage map[string]interface{}) {
	creation, read := usageCacheTokens(usage)
	if creation > 0 {
		record["cache_creation_tokens"] = creation
	}
	if read > 0 {
		record["cache_read_tokens"] = read
	}
}

func relayDebugSSEDataPayloads(event []byte) []string {
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	dataParts := make([]string, 0, len(lines))
	inData := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataParts = append(dataParts, data)
			inData = true
			continue
		}
		if inData && !strings.HasPrefix(line, "event:") && strings.TrimSpace(line) != "" {
			dataParts = append(dataParts, line)
		}
	}
	if len(dataParts) == 0 {
		return nil
	}
	raw := strings.TrimSpace(strings.Join(dataParts, "\n"))
	if raw == "" {
		return nil
	}
	return []string{raw}
}

func relayDebugSSEEventLine(event []byte, prefix string) string {
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func copyIfPresent(dst map[string]interface{}, src map[string]interface{}, srcKey, dstKey string) {
	if value, ok := src[srcKey]; ok {
		dst[dstKey] = value
	}
}

func relayDebugPreview(body []byte, limit int) string {
	s := strings.TrimSpace(string(body))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

// isDebugNullChunk detects OpenAI SSE chunks that carry no meaningful content.
// These are empty heartbeat chunks sent by some providers (e.g. during
// reasoning) with content:null, tool_calls:null, and no finish_reason.
func isDebugNullChunk(body []byte) bool {
	if len(body) == 0 {
		return true
	}
	// Quick byte-level check before JSON parsing.
	// Null chunks contain "content":null and no "usage" key.
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"content":null`) && !strings.Contains(bodyStr, `"content": null`) {
		return false
	}
	if strings.Contains(bodyStr, `"finish_reason"`) {
		finishIdx := strings.Index(bodyStr, `"finish_reason":`)
		if finishIdx >= 0 {
			after := bodyStr[finishIdx+15:]
			after = strings.TrimSpace(after)
			if len(after) > 0 && after[0] != 'n' && after[0] != 'N' {
				// finish_reason is a non-null string → not a null chunk
				return false
			}
		}
	}
	if strings.Contains(bodyStr, `"usage"`) {
		return false
	}
	if strings.Contains(bodyStr, `"tool_calls"`) {
		// Check if tool_calls has actual content (not null).
		tcIdx := strings.Index(bodyStr, `"tool_calls":`)
		if tcIdx >= 0 {
			after := bodyStr[tcIdx+12:]
			after = strings.TrimSpace(after)
			if len(after) > 0 && after[0] == '[' {
				return false // has actual tool_calls array
			}
		}
	}
	return true
}
