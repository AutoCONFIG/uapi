package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/valyala/fasthttp"
)

func (h *Handler) RemoteDebugDumps(ctx *fasthttp.RequestCtx) {
	if h.cfg == nil || !h.cfg.DebugDump.AcceptRemote {
		h.jsonError(ctx, fasthttp.StatusNotFound, "remote dumps disabled")
		return
	}
	dir := strings.TrimSpace(h.cfg.DebugDump.Dir)
	if dir == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "debug dump dir is not configured")
		return
	}
	maxBytes := h.cfg.DebugDump.MaxUploadBytesMB * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 16 * 1024 * 1024
	}
	body := ctx.PostBody()
	if len(body) == 0 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "empty dump")
		return
	}
	if len(body) > maxBytes {
		h.jsonError(ctx, fasthttp.StatusRequestEntityTooLarge, "dump upload too large")
		return
	}
	relayNodeID := sanitizeDumpName(string(ctx.Request.Header.Peek("X-UAPI-Relay-Node-ID")))
	if relayNodeID == "" {
		relayNodeID = "unknown-relay"
	}
	day := time.Now().Local().Format("2006-01-02")
	archiveDir := filepath.Join(filepath.Clean(dir), day, "relay", relayNodeID, "archives")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		logger.Warnf("remote.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", archiveDir))
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create dump dir failed")
		return
	}
	dumpID := sanitizeDumpName(string(ctx.Request.Header.Peek("X-UAPI-Dump-ID")))
	if dumpID == "" {
		dumpID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	name := time.Now().Local().Format("20060102T150405.000000000-0700") + "-" + dumpID + ".tar.gz"
	path := filepath.Join(archiveDir, name)
	if err := os.WriteFile(path, body, 0600); err != nil {
		logger.Warnf("remote.debug_dump", "write dump failed", logger.Err(err), logger.F("path", path))
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "write dump failed")
		return
	}
	extractDir := filepath.Join(filepath.Clean(dir), day, "relay", relayNodeID, "remote", strings.TrimSuffix(name, ".tar.gz"))
	if err := debugdump.ExtractTarGz(extractDir, body); err != nil {
		logger.Warnf("remote.debug_dump", "extract remote dump failed", logger.Err(err), logger.F("path", path))
	} else {
		writeRemoteDumpIndex(time.Now(), filepath.Clean(dir), relayNodeID, dumpID, extractDir, path, body)
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"accepted": true})
}

func writeRemoteDumpIndex(now time.Time, baseDir, relayNodeID, dumpID, extractDir, archivePath string, body []byte) {
	summary := remoteDumpSummary(body)
	entry := debugdump.Entry{
		Side:           "relay",
		Category:       remoteDumpCategory(summary),
		Span:           "relay." + remoteDumpCategory(summary),
		TraceID:        dumpID,
		RelayRequestID: remoteDumpString(summary, "relay_request_id", dumpID),
		RelayNodeID:    relayNodeID,
		Method:         remoteDumpString(summary, "method", ""),
		URL:            remoteDumpString(summary, "url", ""),
		Endpoint:       remoteDumpString(summary, "endpoint", ""),
		Status:         remoteDumpInt(summary, "status_code"),
		LatencyMS:      int64(remoteDumpInt(summary, "latency_ms")),
		Model:          remoteDumpString(summary, "model", ""),
		RoutedModel:    remoteDumpString(summary, "routed_model", ""),
		ChannelID:      remoteDumpString(summary, "channel_id", ""),
		AccountID:      remoteDumpString(summary, "account_id", ""),
		Error:          remoteDumpString(summary, "error", ""),
		DumpPath:       filepath.ToSlash(strings.TrimPrefix(extractDir, baseDir+string(os.PathSeparator))),
		Extra: map[string]interface{}{
			"archive_path": filepath.ToSlash(strings.TrimPrefix(archivePath, baseDir+string(os.PathSeparator))),
			"remote":       true,
		},
	}
	if gatewayRequest := remoteDumpString(summary, "gateway_request", ""); gatewayRequest != "" {
		entry.GatewayRequestID = gatewayRequest
	}
	debugdump.AppendIndex(now, entry)
}

func remoteDumpSummary(body []byte) map[string]interface{} {
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return nil
		}
		if filepath.Base(header.Name) != "summary.json" {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, 1024*1024))
		if err != nil {
			return nil
		}
		var out map[string]interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out
	}
}

func remoteDumpCategory(summary map[string]interface{}) string {
	if kind := remoteDumpString(summary, "kind", ""); kind == "internal_exchange" {
		switch strings.ToLower(remoteDumpString(summary, "direction", "")) {
		case "relay_to_gateway":
			return "to-gateway"
		case "gateway_to_relay":
			return "from-gateway"
		default:
			return "control"
		}
	}
	return "to-upstream"
}

func remoteDumpString(summary map[string]interface{}, key, fallback string) string {
	if summary == nil {
		return fallback
	}
	if value, ok := summary[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func remoteDumpInt(summary map[string]interface{}, key string) int {
	if summary == nil {
		return 0
	}
	switch value := summary[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func sanitizeDumpName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
