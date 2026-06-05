package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/google/uuid"
)

const (
	relayDebugDumpDirEnv        = "UAPI_RELAY_DEBUG_DUMP_DIR"
	relayDebugStreamFileMaxSize = 2 * 1024 * 1024
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warnf("relay.debug_dump", "read dump dir before cleanup failed", logger.Err(err), logger.F("dir", dir))
		return
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			logger.Warnf("relay.debug_dump", "remove old dump entry failed", logger.Err(err), logger.F("dir", dir), logger.F("entry", name))
			continue
		}
		removed++
	}
	logger.Infof("relay.debug_dump", "old dump entries cleaned", logger.F("dir", dir), logger.F("removed", removed))
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
	f, err := os.OpenFile(filepath.Join(t.Dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warnf("relay.debug_dump", "open events file failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir))
		return
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		logger.Warnf("relay.debug_dump", "write event failed", logger.Err(err), logger.F("relay_request_id", t.ID), logger.F("dump_dir", t.Dir))
	}
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
