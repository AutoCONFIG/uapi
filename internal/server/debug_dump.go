package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const gatewayDebugBodyLimit = 32 * 1024

type gatewayDebugTrace struct {
	id       string
	started  time.Time
	category string
	dir      string
	method   string
	path     string
	reqBody  []byte
	reqBytes int
	reqHead  map[string]string
}

func startGatewayDebugDump(ctx *fasthttp.RequestCtx, method, requestPath string, started time.Time) *gatewayDebugTrace {
	if !debugdump.Enabled() {
		return nil
	}
	category := gatewayDebugCategory(requestPath)
	if category == "" {
		return nil
	}
	id := "gw_" + uuid.NewString()
	dir := debugdump.EntryDir(started, "gateway", category, debugdump.EntryName(started, id))
	if dir == "" {
		return nil
	}
	trace := &gatewayDebugTrace{
		id:       id,
		started:  started,
		category: category,
		dir:      dir,
		method:   method,
		path:     requestPath,
		reqHead:  gatewayDebugRequestHeaders(ctx),
	}
	if requestPath != "/internal/dumps" {
		body := ctx.PostBody()
		trace.reqBytes = len(body)
		trace.reqBody = gatewayDebugBodyPreview(body)
	}
	debugdump.WriteJSON(dir, "request.headers.json", trace.reqHead)
	if len(trace.reqBody) > 0 {
		debugdump.WriteFile(dir, "request.body.json", trace.reqBody)
	}
	return trace
}

func (t *gatewayDebugTrace) Finish(ctx *fasthttp.RequestCtx) {
	if t == nil {
		return
	}
	now := time.Now()
	status := ctx.Response.StatusCode()
	respHead := gatewayDebugResponseHeaders(ctx)
	respBody := ctx.Response.Body()
	debugdump.WriteJSON(t.dir, "response.headers.json", respHead)
	if len(respBody) > 0 {
		debugdump.WriteFile(t.dir, "response.body.json", gatewayDebugBodyPreview(respBody))
	}
	summary := map[string]interface{}{
		"timestamp":          t.started.Local().Format(time.RFC3339Nano),
		"gateway_request_id": t.id,
		"side":               "gateway",
		"category":           t.category,
		"method":             t.method,
		"path":               t.path,
		"status_code":        status,
		"latency_ms":         now.Sub(t.started).Milliseconds(),
		"request_bytes":      t.reqBytes,
		"response_bytes":     len(respBody),
	}
	debugdump.WriteJSON(t.dir, "summary.json", summary)
	debugdump.AppendIndex(t.started, debugdump.Entry{
		Side:             "gateway",
		Category:         t.category,
		Span:             "gateway." + t.category,
		TraceID:          t.id,
		GatewayRequestID: t.id,
		Method:           t.method,
		Path:             t.path,
		Status:           status,
		LatencyMS:        now.Sub(t.started).Milliseconds(),
		DumpPath:         gatewayDebugRelativePath(t.dir),
	})
}

func gatewayDebugCategory(requestPath string) string {
	switch {
	case requestPath == "/healthz":
		return ""
	case strings.HasPrefix(requestPath, "/api/"):
		return "api"
	case strings.HasPrefix(requestPath, "/v1/"), requestPath == "/v1", strings.HasPrefix(requestPath, "/v1beta/"), requestPath == "/v1beta":
		return "downstream"
	case strings.HasPrefix(requestPath, "/internal/"):
		if requestPath == "/internal/dumps" {
			return "internal"
		}
		return "internal"
	default:
		return ""
	}
}

func gatewayDebugRequestHeaders(ctx *fasthttp.RequestCtx) map[string]string {
	headers := map[string]string{}
	ctx.Request.Header.VisitAll(func(k, v []byte) {
		key := string(k)
		headers[key] = gatewayDebugHeaderValue(key, string(v))
	})
	return headers
}

func gatewayDebugResponseHeaders(ctx *fasthttp.RequestCtx) map[string]string {
	headers := map[string]string{}
	ctx.Response.Header.VisitAll(func(k, v []byte) {
		key := string(k)
		headers[key] = gatewayDebugHeaderValue(key, string(v))
	})
	return headers
}

func gatewayDebugHeaderValue(key, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization", "cookie", "set-cookie", "x-api-key", "x-uapi-internal-secret", "x-uapi-signature":
		return "[redacted]"
	default:
		if strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "key") {
			return "[redacted]"
		}
		return value
	}
}

func gatewayDebugBodyPreview(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	if len(body) > gatewayDebugBodyLimit {
		out := append([]byte(nil), body[:gatewayDebugBodyLimit]...)
		out = append(out, []byte("\n...[truncated]")...)
		return out
	}
	var value interface{}
	if err := json.Unmarshal(body, &value); err == nil {
		sanitized := gatewayDebugSanitizeValue("", value)
		if raw, err := json.MarshalIndent(sanitized, "", "  "); err == nil {
			return append(raw, '\n')
		}
	}
	return append([]byte(nil), body...)
}

func gatewayDebugSanitizeValue(key string, value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for childKey, childValue := range v {
			if gatewayDebugSensitiveKey(childKey) {
				out[childKey] = "[redacted]"
				continue
			}
			out[childKey] = gatewayDebugSanitizeValue(childKey, childValue)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = gatewayDebugSanitizeValue(key, v[i])
		}
		return out
	case string:
		if gatewayDebugLargeContentKey(key) && len([]rune(v)) > 256 {
			runes := []rune(v)
			return string(runes[:256]) + "...[truncated]"
		}
		return v
	default:
		return value
	}
}

func gatewayDebugSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	return normalized == "authorization" ||
		normalized == "api_key" ||
		normalized == "apikey" ||
		normalized == "access_token" ||
		normalized == "refresh_token" ||
		normalized == "id_token" ||
		normalized == "cookie" ||
		normalized == "client_secret"
}

func gatewayDebugLargeContentKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	return normalized == "content" || normalized == "text" || normalized == "input" || normalized == "output" || strings.Contains(normalized, "base64")
}

func gatewayDebugRelativePath(path string) string {
	base := debugdump.BaseDir()
	if base == "" {
		return ""
	}
	rel := strings.TrimPrefix(path, filepath.Clean(base)+string(os.PathSeparator))
	return filepath.ToSlash(rel)
}
