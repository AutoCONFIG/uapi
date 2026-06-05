package relay

import (
	"bytes"
	"encoding/json"
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
	"github.com/google/uuid"
)

const (
	relayDebugDumpDirEnv            = "UAPI_RELAY_DEBUG_DUMP_DIR"
	relayDebugDumpMaxAgeEnv         = "UAPI_RELAY_DEBUG_DUMP_MAX_AGE"
	relayDebugDumpMaxEntriesEnv     = "UAPI_RELAY_DEBUG_DUMP_MAX_ENTRIES"
	relayDebugDumpDefaultMaxAge     = 3 * time.Hour
	relayDebugDumpDefaultMaxEntries = 200
	relayDebugStreamFileMaxSize     = 2 * 1024 * 1024
)

var relayDebugDumpDir = os.Getenv(relayDebugDumpDirEnv)

func init() {
	cleanupRelayDebugDumpDir()
}

func relayDebugDumpEnabled() bool {
	return relayDebugDumpDir != ""
}

func cleanupRelayDebugDumpDir() {
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
	maxAge := relayDebugDumpMaxAge()
	maxEntries := relayDebugDumpMaxEntries()
	removed, kept, err := cleanupRelayDebugDumpDirWithLimits(dir, maxAge, maxEntries, time.Now())
	if err != nil {
		logger.Warnf("relay.debug_dump", "cleanup dump dir failed", logger.Err(err), logger.F("dir", dir))
		return
	}
	logger.Infof("relay.debug_dump", "old dump entries cleaned",
		logger.F("dir", dir),
		logger.F("removed", removed),
		logger.F("kept", kept),
		logger.F("max_age", maxAge.String()),
		logger.F("max_entries", maxEntries),
	)
}

func relayDebugDumpMaxAge() time.Duration {
	raw := os.Getenv(relayDebugDumpMaxAgeEnv)
	if raw == "" {
		return relayDebugDumpDefaultMaxAge
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warnf("relay.debug_dump", "invalid max age, using default", logger.F("env", relayDebugDumpMaxAgeEnv), logger.F("value", raw))
		return relayDebugDumpDefaultMaxAge
	}
	return d
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

func cleanupRelayDebugDumpDirWithLimits(dir string, maxAge time.Duration, maxEntries int, now time.Time) (removed int, kept int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	var remaining []relayDebugDumpEntry
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			logger.Warnf("relay.debug_dump", "stat dump entry failed", logger.Err(infoErr), logger.F("dir", dir), logger.F("entry", name))
			continue
		}
		if maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
			if removeRelayDebugDumpEntry(dir, name) {
				removed++
			}
			continue
		}
		remaining = append(remaining, relayDebugDumpEntry{name: name, modTime: info.ModTime()})
	}
	sort.Slice(remaining, func(i, j int) bool {
		return remaining[i].modTime.After(remaining[j].modTime)
	})
	if maxEntries > 0 && len(remaining) > maxEntries {
		for _, entry := range remaining[maxEntries:] {
			if removeRelayDebugDumpEntry(dir, entry.name) {
				removed++
			}
		}
		remaining = remaining[:maxEntries]
	}
	return removed, len(remaining), nil
}

func removeRelayDebugDumpEntry(dir, name string) bool {
	if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
		logger.Warnf("relay.debug_dump", "remove old dump entry failed", logger.Err(err), logger.F("dir", dir), logger.F("entry", name))
		return false
	}
	return true
}

type relayDebugDumpSummary struct {
	Timestamp      string                 `json:"timestamp"`
	RelayRequestID string                 `json:"relay_request_id"`
	TokenID        string                 `json:"token_id,omitempty"`
	AccountID      string                 `json:"account_id,omitempty"`
	ChannelID      string                 `json:"channel_id,omitempty"`
	GatewayRequest string                 `json:"gateway_request,omitempty"`
	ClientFormat   provider.Format        `json:"client_format"`
	UpstreamFormat provider.Format        `json:"upstream_format"`
	RequestType    string                 `json:"request_type"`
	Model          string                 `json:"model"`
	RoutedModel    string                 `json:"routed_model"`
	Stream         bool                   `json:"stream"`
	OriginalBytes  int                    `json:"original_bytes"`
	ConvertedBytes int                    `json:"converted_bytes"`
	Original       relayDebugRequestStats `json:"original"`
	Converted      relayDebugRequestStats `json:"converted"`
}

type relayDebugRequestStats struct {
	ToolsLen          int             `json:"tools_len,omitempty"`
	MessagesLen       int             `json:"messages_len,omitempty"`
	InputLen          int             `json:"input_len,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls json.RawMessage `json:"parallel_tool_calls,omitempty"`
	HasThinking       bool            `json:"has_thinking,omitempty"`
}

type relayDebugTrace struct {
	ID              string
	Dir             string
	mu              sync.Mutex
	streamBytes     map[string]int
	streamTruncated map[string]bool
	streamEvents    map[string]int
	streamPayloads  map[string]int
	streamLast      map[string]map[string]interface{}
}

func startRelayRequestDebugDump(original, converted []byte, token db.Token, ch *db.Channel, acc *db.Account, claims *internalauth.Claims, clientFormat, upstreamFormat provider.Format, requestType relayRequestType, model, routedModel string, stream bool) *relayDebugTrace {
	if relayDebugDumpDir == "" {
		return nil
	}
	traceID := uuid.NewString()
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + traceID
	outDir := filepath.Join(relayDebugDumpDir, name)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logger.Warnf("relay.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", outDir))
		return nil
	}

	summary := relayDebugDumpSummary{
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		RelayRequestID: traceID,
		TokenID:        token.ID.String(),
		ClientFormat:   clientFormat,
		UpstreamFormat: upstreamFormat,
		RequestType:    string(requestType),
		Model:          model,
		RoutedModel:    routedModel,
		Stream:         stream,
		OriginalBytes:  len(original),
		ConvertedBytes: len(converted),
		Original:       relayDebugRequestStatsFromBody(original),
		Converted:      relayDebugRequestStatsFromBody(converted),
	}
	if ch != nil {
		summary.ChannelID = ch.ID.String()
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
		ID:              traceID,
		Dir:             outDir,
		streamBytes:     map[string]int{},
		streamTruncated: map[string]bool{},
		streamEvents:    map[string]int{},
		streamPayloads:  map[string]int{},
		streamLast:      map[string]map[string]interface{}{},
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
		logger.F("original_tool_choice", relayDebugRawString(summary.Original.ToolChoice)),
		logger.F("converted_tool_choice", relayDebugRawString(summary.Converted.ToolChoice)),
		logger.F("original_parallel_tool_calls", relayDebugRawString(summary.Original.ParallelToolCalls)),
		logger.F("converted_parallel_tool_calls", relayDebugRawString(summary.Converted.ParallelToolCalls)),
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
		}
	}
	if raw := root["messages"]; len(raw) > 0 {
		var messages []json.RawMessage
		if err := json.Unmarshal(raw, &messages); err == nil {
			stats.MessagesLen = len(messages)
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
	if raw := root["thinking"]; len(raw) > 0 && string(raw) != "null" {
		stats.HasThinking = true
	}
	return stats
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
	record := map[string]interface{}{
		"timestamp":        time.Now().UTC().Format(time.RFC3339Nano),
		"relay_request_id": t.ID,
		"event":            name,
	}
	for _, field := range fields {
		if field.Key == "" {
			continue
		}
		record[field.Key] = field.Value
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

func (t *relayDebugTrace) StreamChunk(name string, body []byte) {
	if t == nil || len(body) == 0 || name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

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

func (t *relayDebugTrace) noteStreamTruncatedLocked(name string, written int) {
	if t.streamTruncated[name] {
		return
	}
	t.streamTruncated[name] = true
	record := map[string]interface{}{
		"timestamp":        time.Now().UTC().Format(time.RFC3339Nano),
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
	record["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
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
