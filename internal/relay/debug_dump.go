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
	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const (
	relayDebugDumpMaxAgeEnv           = "UAPI_RELAY_DEBUG_DUMP_MAX_AGE"
	relayDebugDumpMaxEntriesEnv       = "UAPI_RELAY_DEBUG_DUMP_MAX_ENTRIES"
	relayDebugDumpDefaultMaxAge       = 7 * 24 * time.Hour // 7 days
	relayDebugDumpDefaultMaxEntries   = 7                  // keep 7 daily archives
	relayDebugStreamFileMaxSize       = 2 * 1024 * 1024
	relayDebugRequestStringLimit      = 512
	relayDebugRequestLargeStringLimit = 128
	relayDebugRequestRawLimit         = 8 * 1024
)

var (
	relayDebugDumpDir              string
	relayDebugDumpProcessStartedAt = time.Now()
	relayDebugDumpRuntimeMu        sync.RWMutex
	relayDebugDumpRuntime          = relayDebugDumpRuntimeConfig{
		Mode:             "local",
		MaxEntries:       0,
		QueueMaxItems:    1000,
		BatchMaxBytes:    8 * 1024 * 1024,
		UploadTimeout:    10 * time.Second,
		remoteUploadChan: nil,
	}
)

type RelayDebugDumpConfig struct {
	Enabled        bool
	Mode           string
	Dir            string
	MaxEntries     int
	ControlURL     string
	RelayNodeID    string
	InternalSecret string
	QueueMaxItems  int
	BatchMaxBytes  int64
	UploadTimeout  time.Duration
}

type relayDebugDumpRuntimeConfig struct {
	Enabled          bool
	Mode             string
	Dir              string
	MaxEntries       int
	ControlURL       string
	RelayNodeID      string
	InternalSecret   string
	QueueMaxItems    int
	BatchMaxBytes    int64
	UploadTimeout    time.Duration
	remoteUploadChan chan relayDebugRemoteUpload
}

type relayDebugRemoteUpload struct {
	ID   string
	Body []byte
}

type InternalExchangeDump struct {
	Direction       string
	Method          string
	URL             string
	Endpoint        string
	RequestHeaders  map[string]string
	RequestBody     []byte
	StatusCode      int
	ResponseHeaders map[string]string
	ResponseBody    []byte
	ResponseStream  bool
	Err             error
	StartedAt       time.Time
	Latency         time.Duration
}

func ConfigureRelayDebugDump(cfg RelayDebugDumpConfig) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "local"
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = relayDebugDumpDefaultMaxEntries
	}
	queueMax := cfg.QueueMaxItems
	if queueMax <= 0 {
		queueMax = 1000
	}
	batchMax := cfg.BatchMaxBytes
	if batchMax <= 0 {
		batchMax = 8 * 1024 * 1024
	}
	uploadTimeout := cfg.UploadTimeout
	if uploadTimeout <= 0 {
		uploadTimeout = 10 * time.Second
	}

	runtimeCfg := relayDebugDumpRuntimeConfig{
		Enabled:        cfg.Enabled,
		Mode:           mode,
		Dir:            strings.TrimSpace(cfg.Dir),
		MaxEntries:     maxEntries,
		ControlURL:     strings.TrimRight(strings.TrimSpace(cfg.ControlURL), "/"),
		RelayNodeID:    strings.TrimSpace(cfg.RelayNodeID),
		InternalSecret: cfg.InternalSecret,
		QueueMaxItems:  queueMax,
		BatchMaxBytes:  batchMax,
		UploadTimeout:  uploadTimeout,
	}
	if runtimeCfg.Enabled && runtimeCfg.Mode == "remote" {
		runtimeCfg.remoteUploadChan = make(chan relayDebugRemoteUpload, queueMax)
		go relayDebugRemoteUploader(runtimeCfg)
		logger.Infof("relay.debug_dump", "remote debug dump initialized",
			logger.F("relay_node_id", runtimeCfg.RelayNodeID),
			logger.F("queue_max_items", queueMax),
			logger.F("batch_max_bytes", batchMax),
		)
	}

	relayDebugDumpRuntimeMu.Lock()
	relayDebugDumpRuntime = runtimeCfg
	if !runtimeCfg.Enabled {
		relayDebugDumpDir = ""
	} else if runtimeCfg.Mode == "local" {
		relayDebugDumpDir = runtimeCfg.Dir
	} else if runtimeCfg.Mode == "remote" {
		relayDebugDumpDir = ""
	}
	relayDebugDumpRuntimeMu.Unlock()

	if runtimeCfg.Enabled && runtimeCfg.Mode == "local" {
		cleanupRelayDebugDumpDir()
	}
}

func relayDebugRuntime() relayDebugDumpRuntimeConfig {
	relayDebugDumpRuntimeMu.RLock()
	defer relayDebugDumpRuntimeMu.RUnlock()
	return relayDebugDumpRuntime
}

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
	cfg := relayDebugRuntime()
	if cfg.Enabled {
		return cfg.Mode == "remote" || cfg.Dir != ""
	}
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
	cfg := relayDebugRuntime()
	if cfg.Enabled && cfg.MaxEntries > 0 {
		return cfg.MaxEntries
	}
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
	Timestamp          string                                `json:"timestamp"`
	RelayRequestID     string                                `json:"relay_request_id"`
	ProcessID          int                                   `json:"process_id"`
	ProcessStartedAt   string                                `json:"process_started_at"`
	TokenID            string                                `json:"token_id,omitempty"`
	AccountID          string                                `json:"account_id,omitempty"`
	ChannelID          string                                `json:"channel_id,omitempty"`
	ChannelName        string                                `json:"channel_name,omitempty"`
	ChannelType        string                                `json:"channel_type,omitempty"`
	APIFormat          string                                `json:"api_format,omitempty"`
	GatewayRequest     string                                `json:"gateway_request,omitempty"`
	ClientFormat       provider.Format                       `json:"client_format"`
	UpstreamFormat     provider.Format                       `json:"upstream_format"`
	RequestType        string                                `json:"request_type"`
	Model              string                                `json:"model"`
	RoutedModel        string                                `json:"routed_model"`
	Stream             bool                                  `json:"stream"`
	OriginalBytes      int                                   `json:"original_bytes"`
	ConvertedBytes     int                                   `json:"converted_bytes"`
	OriginalDumpBytes  int                                   `json:"original_dump_bytes,omitempty"`
	ConvertedDumpBytes int                                   `json:"converted_dump_bytes,omitempty"`
	CachePassthrough   upstreamconfig.CachePassthroughPolicy `json:"cache_passthrough"`
	Original           relayDebugRequestStats                `json:"original"`
	Converted          relayDebugRequestStats                `json:"converted"`
	RouteAttempts      []map[string]interface{}              `json:"route_attempts,omitempty"`
	FallbackReasons    []string                              `json:"fallback_reasons,omitempty"`
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
	finalOnce       sync.Once
	remote          bool
	files           map[string][]byte
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
	if t == nil {
		return
	}
	info := t.routingInfo
	if info == nil {
		info = map[string]interface{}{}
	}
	if raw, err := json.MarshalIndent(info, "", "  "); err == nil {
		t.WriteFile("routing.json", raw)
	}
}

func relayDebugDumpTimestamp(t time.Time) string {
	return t.Local().Format(time.RFC3339Nano)
}

func relayDebugDumpCurrentDayDir() string {
	cfg := relayDebugRuntime()
	if cfg.Enabled && cfg.Mode == "local" {
		if cfg.Dir == "" {
			return ""
		}
		day := time.Now().Local().Format("2006-01-02")
		return filepath.Join(cfg.Dir, day)
	}
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
	cfg := relayDebugRuntime()
	traceID := uuid.NewString()
	now := time.Now()
	name := relayDebugDumpEntryName(now, traceID)
	outDir := ""
	remote := cfg.Enabled && cfg.Mode == "remote"
	if remote {
		outDir = "remote:" + name
	} else {
		dayDir := relayDebugDumpCurrentDayDir()
		if dayDir == "" {
			return nil
		}
		outDir = filepath.Join(dayDir, "relay", relayDebugNodeDir(cfg), "to-upstream", name)
		if err := os.MkdirAll(outDir, 0755); err != nil {
			logger.Warnf("relay.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", outDir))
			return nil
		}
	}

	dumpedOriginal := relayDebugDumpRequestBody(original)
	dumpedConverted := relayDebugDumpRequestBody(converted)
	summary := relayDebugDumpSummary{
		Timestamp:          relayDebugDumpTimestamp(now),
		RelayRequestID:     traceID,
		ProcessID:          os.Getpid(),
		ProcessStartedAt:   relayDebugDumpTimestamp(relayDebugDumpProcessStartedAt),
		TokenID:            token.ID.String(),
		ClientFormat:       clientFormat,
		UpstreamFormat:     upstreamFormat,
		RequestType:        string(requestType),
		Model:              model,
		RoutedModel:        routedModel,
		Stream:             stream,
		OriginalBytes:      len(original),
		ConvertedBytes:     len(converted),
		OriginalDumpBytes:  len(dumpedOriginal),
		ConvertedDumpBytes: len(dumpedConverted),
		CachePassthrough:   upstreamconfig.CachePassthroughPolicyForChannel(ch, upstreamFormat),
		Original:           relayDebugRequestStatsFromBody(original),
		Converted:          relayDebugRequestStatsFromBody(converted),
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

	trace := &relayDebugTrace{
		ID:                     traceID,
		Dir:                    outDir,
		startedAt:              now,
		remote:                 remote,
		files:                  map[string][]byte{},
		streamBytes:            map[string]int{},
		streamTruncated:        map[string]bool{},
		streamEvents:           map[string]int{},
		streamPayloads:         map[string]int{},
		streamLast:             map[string]map[string]interface{}{},
		streamFirstAt:          map[string]time.Time{},
		streamLastAt:           map[string]time.Time{},
		streamMaxGap:           map[string]time.Duration{},
		streamConsecutiveNulls: map[string]int{},
		streamLastWasNull:      map[string]bool{},
	}
	trace.WriteFile("request.original.json", dumpedOriginal)
	trace.WriteFile("request.converted.json", dumpedConverted)
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		trace.WriteFile("summary.json", raw)
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
	debugdump.AppendIndex(now, debugdump.Entry{
		Side:             "relay",
		Category:         "to-upstream",
		Span:             "relay.to-upstream",
		TraceID:          traceID,
		GatewayRequestID: summary.GatewayRequest,
		RelayRequestID:   traceID,
		RelayNodeID:      relayDebugNodeDir(cfg),
		Model:            model,
		RoutedModel:      routedModel,
		ChannelID:        summary.ChannelID,
		AccountID:        summary.AccountID,
		DumpPath:         relayDebugRelativeDumpPath(outDir),
		Extra: map[string]interface{}{
			"request_type":    string(requestType),
			"client_format":   string(clientFormat),
			"upstream_format": string(upstreamFormat),
			"stream":          stream,
		},
	})
	return trace
}

func relayDebugNodeDir(cfg relayDebugDumpRuntimeConfig) string {
	if name := debugdump.SafeName(cfg.RelayNodeID); name != "" {
		return name
	}
	return "local"
}

func relayDebugRelativeDumpPath(path string) string {
	cfg := relayDebugRuntime()
	base := strings.TrimSpace(cfg.Dir)
	if base == "" {
		base = relayDebugDumpDir
	}
	base = filepath.Clean(base)
	rel := strings.TrimPrefix(path, base+string(os.PathSeparator))
	return filepath.ToSlash(rel)
}

func relayDebugDumpRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	return relayDebugDumpRequestBodyPreview(body)
}

func relayDebugDumpRequestBodyPreview(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var root interface{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return relayDebugDumpRawPreview(body)
	}
	sanitized := relayDebugSanitizeRequestValue("", root)
	raw, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return relayDebugDumpRawPreview(body)
	}
	return append(raw, '\n')
}

func relayDebugDumpRawPreview(body []byte) []byte {
	if len(body) <= relayDebugRequestRawLimit {
		return append([]byte(nil), body...)
	}
	prefix := append([]byte(nil), body[:relayDebugRequestRawLimit]...)
	prefix = append(prefix, []byte(fmt.Sprintf("\n...[truncated %d bytes]", len(body)-relayDebugRequestRawLimit))...)
	return prefix
}

func relayDebugSanitizeRequestValue(key string, value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for childKey, childValue := range v {
			if relayDebugSensitiveJSONKey(childKey) {
				out[childKey] = "[redacted]"
				continue
			}
			out[childKey] = relayDebugSanitizeRequestValue(childKey, childValue)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = relayDebugSanitizeRequestValue(key, v[i])
		}
		return out
	case string:
		return relayDebugTruncateRequestString(key, v)
	default:
		return value
	}
}

func relayDebugSensitiveJSONKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "authorization", "api_key", "apikey", "x_api_key", "access_token", "refresh_token",
		"id_token", "client_secret", "client_assertion", "code_verifier", "cookie", "set_cookie":
		return true
	default:
		return false
	}
}

func relayDebugTruncateRequestString(key, value string) string {
	limit := relayDebugRequestStringLimit
	if relayDebugLargeContentKey(key) {
		limit = relayDebugRequestLargeStringLimit
	}
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + fmt.Sprintf("...[truncated %d chars]", len(runes)-limit)
}

func relayDebugLargeContentKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "content", "text", "input", "output", "data", "image", "image_url", "audio", "video",
		"file", "file_data", "inline_data", "cached_content", "encrypted_content", "redacted_content":
		return true
	default:
		return strings.Contains(normalized, "base64") ||
			strings.Contains(normalized, "blob") ||
			strings.Contains(normalized, "bytes")
	}
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

func (t *relayDebugTrace) WriteFile(name string, body []byte) {
	if t == nil || name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeFileLocked(name, body)
}

func (t *relayDebugTrace) writeFileLocked(name string, body []byte) {
	if t.remote {
		t.files[name] = append([]byte(nil), body...)
		return
	}
	if t.Dir == "" {
		return
	}
	writeRelayDebugFile(t.Dir, name, body)
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
	t.writeJSONLRawLocked("events.jsonl", raw)
	t.mu.Unlock()
	if relayDebugTerminalEvent(name) {
		t.FinalizeRemoteDump()
	}
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

func relayDebugTerminalEvent(name string) bool {
	return strings.HasPrefix(name, "stream_result_") ||
		strings.HasPrefix(name, "buffered_result_") ||
		strings.HasPrefix(name, "force_stream_result_") ||
		strings.HasPrefix(name, "media_result_") ||
		name == "route_failed"
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

	if t.remote {
		t.files[name] = append(t.files[name], chunk...)
	} else {
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
		if err := f.Close(); err != nil {
			logger.Warnf("relay.debug_dump", "close stream dump failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir), logger.F("file", name))
		}
	}
	t.streamBytes[name] += len(chunk)
	if truncated {
		if t.remote {
			t.files[name] = append(t.files[name], []byte("\n\n# UAPI debug stream truncated\n")...)
		} else if f, err := os.OpenFile(filepath.Join(t.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			_, _ = f.Write([]byte("\n\n# UAPI debug stream truncated\n"))
			_ = f.Close()
		}
		t.noteStreamTruncatedLocked(name, t.streamBytes[name])
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
	t.writeJSONLRawLocked("events.jsonl", raw)
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
	if t.remote {
		t.files[name] = append(t.files[name], append(raw, '\n')...)
		return
	}
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

func (t *relayDebugTrace) FinalizeRemoteDump() {
	if t == nil || !t.remote {
		return
	}
	t.finalOnce.Do(func() {
		cfg := relayDebugRuntime()
		if cfg.remoteUploadChan == nil {
			return
		}
		t.mu.Lock()
		files := make(map[string][]byte, len(t.files))
		for name, body := range t.files {
			files[name] = append([]byte(nil), body...)
		}
		t.mu.Unlock()
		body, err := createRelayDebugTarGzBytes(t.ID, files)
		if err != nil {
			logger.Warnf("relay.debug_dump", "create remote archive failed", logger.Err(err), logger.F("relay_request_id", t.ID))
			return
		}
		if cfg.BatchMaxBytes > 0 && int64(len(body)) > cfg.BatchMaxBytes {
			logger.Warnf("relay.debug_dump", "drop remote archive over limit",
				logger.F("relay_request_id", t.ID),
				logger.F("bytes", len(body)),
				logger.F("limit", cfg.BatchMaxBytes),
			)
			return
		}
		select {
		case cfg.remoteUploadChan <- relayDebugRemoteUpload{ID: t.ID, Body: body}:
		default:
			logger.Warnf("relay.debug_dump", "drop remote archive because upload queue is full",
				logger.F("relay_request_id", t.ID),
				logger.F("queue_max_items", cfg.QueueMaxItems),
			)
		}
	})
}

func createRelayDebugTarGzBytes(root string, files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	names := make([]string, 0, len(files))
	for name := range files {
		if clean := cleanRelayDebugArchiveName(name); clean != "" {
			names = append(names, clean)
		}
	}
	sort.Strings(names)
	prefix := cleanRelayDebugArchiveName(root)
	if prefix == "" {
		prefix = "dump"
	}
	now := time.Now()
	for _, name := range names {
		body := files[name]
		header := &tar.Header{
			Name:    pathJoinArchive(prefix, name),
			Mode:    0600,
			Size:    int64(len(body)),
			ModTime: now,
		}
		if err := tw.WriteHeader(header); err != nil {
			_ = tw.Close()
			_ = gzw.Close()
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			_ = tw.Close()
			_ = gzw.Close()
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		_ = gzw.Close()
		return nil, err
	}
	if err := gzw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func cleanRelayDebugArchiveName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return ""
	}
	return strings.ReplaceAll(clean, "\\", "/")
}

func pathJoinArchive(elem ...string) string {
	return strings.ReplaceAll(filepath.Join(elem...), "\\", "/")
}

func relayDebugRemoteUploader(cfg relayDebugDumpRuntimeConfig) {
	for item := range cfg.remoteUploadChan {
		if len(item.Body) == 0 {
			continue
		}
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		req.SetRequestURI(cfg.ControlURL + "/internal/dumps")
		req.Header.SetMethod(fasthttp.MethodPost)
		req.Header.Set("X-UAPI-Internal-Secret", cfg.InternalSecret)
		req.Header.Set("X-UAPI-Relay-Node-ID", cfg.RelayNodeID)
		req.Header.Set("X-UAPI-Dump-ID", item.ID)
		req.Header.SetContentType("application/gzip")
		req.SetBodyRaw(item.Body)
		err := bufferedClient.DoTimeout(req, resp, cfg.UploadTimeout)
		status := resp.StatusCode()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		if err != nil {
			logger.Warnf("relay.debug_dump", "remote upload failed", logger.Err(err), logger.F("relay_request_id", item.ID))
			continue
		}
		if status >= 300 {
			logger.Warnf("relay.debug_dump", "remote upload rejected", logger.F("relay_request_id", item.ID), logger.F("status", status))
			continue
		}
		logger.Debugf("relay.debug_dump", "remote upload accepted", logger.F("relay_request_id", item.ID), logger.F("bytes", len(item.Body)))
	}
}

func RecordInternalExchangeDump(exchange InternalExchangeDump) {
	if !relayDebugDumpEnabled() {
		return
	}
	cfg := relayDebugRuntime()
	if cfg.Enabled && cfg.Mode == "remote" && cfg.remoteUploadChan == nil {
		return
	}
	now := time.Now()
	if exchange.StartedAt.IsZero() {
		exchange.StartedAt = now
	}
	traceID := uuid.NewString()
	name := relayDebugDumpEntryName(now, traceID)
	summary := map[string]interface{}{
		"timestamp":        relayDebugDumpTimestamp(now),
		"relay_request_id": traceID,
		"kind":             "internal_exchange",
		"direction":        exchange.Direction,
		"method":           exchange.Method,
		"url":              relayDebugRedactedURL(exchange.URL),
		"endpoint":         exchange.Endpoint,
		"status_code":      exchange.StatusCode,
		"response_stream":  exchange.ResponseStream,
		"started_at":       relayDebugDumpTimestamp(exchange.StartedAt),
		"latency_ms":       exchange.Latency.Milliseconds(),
		"request_headers":  relayDebugSanitizeHeaderMap(exchange.RequestHeaders),
		"response_headers": relayDebugSanitizeHeaderMap(exchange.ResponseHeaders),
		"request_bytes":    len(exchange.RequestBody),
		"response_bytes":   len(exchange.ResponseBody),
	}
	if exchange.Err != nil {
		summary["error"] = exchange.Err.Error()
	}
	files := map[string][]byte{}
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		files["summary.json"] = raw
	}
	if len(exchange.RequestBody) > 0 {
		files["request.body.json"] = relayDebugDumpRequestBodyPreview(exchange.RequestBody)
	}
	if len(exchange.ResponseBody) > 0 {
		files["response.body.json"] = relayDebugDumpRequestBodyPreview(exchange.ResponseBody)
	}
	if cfg.Enabled && cfg.Mode == "remote" {
		body, err := createRelayDebugTarGzBytes(traceID, files)
		if err != nil {
			logger.Warnf("relay.debug_dump", "create internal exchange archive failed", logger.Err(err), logger.F("relay_request_id", traceID))
			return
		}
		if cfg.BatchMaxBytes > 0 && int64(len(body)) > cfg.BatchMaxBytes {
			logger.Warnf("relay.debug_dump", "drop internal exchange archive over limit", logger.F("relay_request_id", traceID), logger.F("bytes", len(body)), logger.F("limit", cfg.BatchMaxBytes))
			return
		}
		select {
		case cfg.remoteUploadChan <- relayDebugRemoteUpload{ID: traceID, Body: body}:
		default:
			logger.Warnf("relay.debug_dump", "drop internal exchange archive because upload queue is full", logger.F("relay_request_id", traceID), logger.F("queue_max_items", cfg.QueueMaxItems))
		}
		return
	}
	dayDir := relayDebugDumpCurrentDayDir()
	if dayDir == "" {
		return
	}
	category := relayDebugInternalExchangeCategory(exchange.Direction)
	side := relayDebugInternalExchangeSide(exchange.Direction)
	var outDir string
	if side == "gateway" {
		outDir = filepath.Join(dayDir, "gateway", category, name)
	} else {
		outDir = filepath.Join(dayDir, "relay", relayDebugNodeDir(cfg), category, name)
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logger.Warnf("relay.debug_dump", "create internal exchange dump dir failed", logger.Err(err), logger.F("dir", outDir))
		return
	}
	for fileName, body := range files {
		writeRelayDebugFile(outDir, fileName, body)
	}
	debugdump.AppendIndex(now, debugdump.Entry{
		Side:           side,
		Category:       category,
		Span:           side + "." + category,
		TraceID:        traceID,
		RelayRequestID: traceID,
		RelayNodeID:    relayDebugNodeDir(cfg),
		Method:         exchange.Method,
		URL:            relayDebugRedactedURL(exchange.URL),
		Endpoint:       exchange.Endpoint,
		Status:         exchange.StatusCode,
		LatencyMS:      exchange.Latency.Milliseconds(),
		DumpPath:       relayDebugRelativeDumpPath(outDir),
		Extra: map[string]interface{}{
			"direction":       exchange.Direction,
			"response_stream": exchange.ResponseStream,
		},
	})
}

func relayDebugInternalExchangeCategory(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "gateway_to_relay":
		return "to-relay"
	case "relay_to_gateway":
		return "to-gateway"
	default:
		return "control"
	}
}

func relayDebugInternalExchangeSide(direction string) string {
	if strings.EqualFold(strings.TrimSpace(direction), "gateway_to_relay") {
		return "gateway"
	}
	return "relay"
}

func HeaderMapFromRequest(req *fasthttp.Request) map[string]string {
	if req == nil {
		return nil
	}
	headers := map[string]string{}
	req.Header.VisitAll(func(k, v []byte) {
		headers[string(k)] = string(v)
	})
	return headers
}

func HeaderMapFromResponse(resp *fasthttp.Response) map[string]string {
	if resp == nil {
		return nil
	}
	headers := map[string]string{}
	resp.Header.VisitAll(func(k, v []byte) {
		headers[string(k)] = string(v)
	})
	return headers
}

func relayDebugSanitizeHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "authorization" ||
			lower == "cookie" ||
			lower == "set-cookie" ||
			lower == "x-uapi-internal-secret" ||
			lower == "x-uapi-signature" ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "credential") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}
