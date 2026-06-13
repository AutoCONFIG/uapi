package oauthdebug

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/google/uuid"
)

var dumpConfig struct {
	mu  sync.RWMutex
	dir string
}

func Configure(dir string) {
	dumpConfig.mu.Lock()
	defer dumpConfig.mu.Unlock()
	dumpConfig.dir = strings.TrimSpace(dir)
}

func DumpDir() string {
	dumpConfig.mu.RLock()
	defer dumpConfig.mu.RUnlock()
	return dumpConfig.dir
}

type Dump struct {
	Timestamp string                 `json:"timestamp"`
	Provider  string                 `json:"provider"`
	Operation string                 `json:"operation,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Upstream  interface{}            `json:"upstream,omitempty"`
	Result    interface{}            `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

type HTTPDebug struct {
	Request  HTTPDebugRequest  `json:"request"`
	Response HTTPDebugResponse `json:"response"`
}

type HTTPDebugRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

type HTTPDebugResponse struct {
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBytes  int                 `json:"body_bytes,omitempty"`
	Body       string              `json:"body,omitempty"`
}

func Write(provider, operation string, metadata map[string]interface{}, upstream interface{}, result interface{}, err error) {
	baseDir := DumpDir()
	if baseDir == "" {
		return
	}
	now := time.Now()
	name := now.Local().Format("20060102T150405.000000000-0700") + "-oauth-" + safeName(provider)
	if operation != "" {
		name += "-" + safeName(operation)
	}
	name += "-" + uuid.NewString()
	outDir := filepath.Join(filepath.Clean(baseDir), now.Local().Format("2006-01-02"), "oauth", safeName(provider), safeName(operation), name)
	if mkErr := os.MkdirAll(outDir, 0755); mkErr != nil {
		logger.Warnf("oauth.debug_dump", "create oauth dump dir failed", logger.Err(mkErr), logger.F("dir", outDir))
		return
	}
	record := Dump{
		Timestamp: now.Local().Format(time.RFC3339Nano),
		Provider:  provider,
		Operation: operation,
		Metadata:  RedactMap(metadata),
		Upstream:  RedactValue(upstream),
		Result:    RedactValue(result),
	}
	if err != nil {
		record.Error = RedactText(err.Error())
	}
	raw, marshalErr := json.MarshalIndent(record, "", "  ")
	if marshalErr != nil {
		logger.Warnf("oauth.debug_dump", "marshal oauth dump failed", logger.Err(marshalErr), logger.F("dir", outDir))
		return
	}
	if writeErr := os.WriteFile(filepath.Join(outDir, "oauth.json"), raw, 0644); writeErr != nil {
		logger.Warnf("oauth.debug_dump", "write oauth dump failed", logger.Err(writeErr), logger.F("dir", outDir))
	}
	debugdump.AppendIndex(now, debugdump.Entry{
		Side:     "gateway",
		Category: "oauth",
		Span:     "oauth." + safeName(operation),
		Path:     operation,
		Status:   oauthDebugStatus(err),
		Error:    record.Error,
		DumpPath: filepath.ToSlash(strings.TrimPrefix(outDir, filepath.Clean(baseDir)+string(os.PathSeparator))),
		Extra: map[string]interface{}{
			"provider":  provider,
			"operation": operation,
		},
	})
}

func oauthDebugStatus(err error) int {
	if err != nil {
		return 500
	}
	return 200
}

func NewHTTPDebug(req *http.Request, requestBody []byte) *HTTPDebug {
	if req == nil {
		return nil
	}
	debugInfo := &HTTPDebug{
		Request: HTTPDebugRequest{
			Method:  req.Method,
			URL:     RedactURL(req.URL.String()),
			Headers: RedactRequestHeaders(req.Header),
		},
	}
	if len(requestBody) > 0 {
		debugInfo.Request.Body = BodyPreview(requestBody, 10000)
	}
	return debugInfo
}

func FinishHTTPDebug(debugInfo *HTTPDebug, resp *http.Response, responseBody []byte) {
	if debugInfo == nil || resp == nil {
		return
	}
	debugInfo.Response.StatusCode = resp.StatusCode
	debugInfo.Response.Headers = RedactResponseHeaders(resp.Header)
	debugInfo.Response.BodyBytes = len(responseBody)
	debugInfo.Response.Body = BodyPreview(responseBody, 10000)
}

func RedactMap(metadata map[string]interface{}) map[string]interface{} {
	if metadata == nil {
		return nil
	}
	out := make(map[string]interface{}, len(metadata))
	for key, value := range metadata {
		if IsSecretKey(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = RedactValue(value)
	}
	return out
}

func RedactValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return RedactMap(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = RedactValue(item)
		}
		return out
	case string:
		return RedactText(v)
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			var decoded interface{}
			if json.Unmarshal(raw, &decoded) == nil {
				switch decoded.(type) {
				case map[string]interface{}, []interface{}:
					return RedactValue(decoded)
				}
			}
		}
		return value
	}
}

func RedactRequestHeaders(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if IsSecretHeader(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = RedactText(strings.Join(values, ", "))
	}
	return out
}

func RedactResponseHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if IsSecretHeader(key) {
			out[key] = []string{"[redacted]"}
			continue
		}
		copied := make([]string, len(values))
		for i, value := range values {
			copied[i] = RedactText(value)
		}
		out[key] = copied
	}
	return out
}

func IsSecretHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "authorization", "x-api-key", "api-key", "anthropic-api-key", "x-goog-api-key":
		return true
	default:
		return false
	}
}

func IsSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "access_token", "refresh_token", "id_token", "raw_id_token", "api_key", "apikey":
		return true
	}
	compact := strings.ReplaceAll(normalized, "_", "")
	switch compact {
	case "accesstoken", "refreshtoken", "idtoken", "rawidtoken", "apikey":
		return true
	}
	return false
}

func BodyPreview(body []byte, limit int) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	text = RedactText(text)
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	if text == "" {
		return "empty body"
	}
	return text
}

func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return RedactText(raw)
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if IsSecretKey(key) {
			query.Set(key, "[redacted]")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return RedactText(parsed.String())
}

func RedactText(text string) string {
	if text == "" {
		return text
	}
	var value interface{}
	if json.Unmarshal([]byte(text), &value) == nil {
		switch value.(type) {
		case map[string]interface{}, []interface{}:
			if redacted, err := json.Marshal(RedactValue(value)); err == nil {
				return string(redacted)
			}
		}
	}
	if form, err := url.ParseQuery(text); err == nil && len(form) > 0 && strings.Contains(text, "=") {
		changed := false
		for key := range form {
			if IsSecretKey(key) {
				form.Set(key, "[redacted]")
				changed = true
			}
		}
		if changed {
			return form.Encode()
		}
	}
	for _, key := range []string{"access_token", "refresh_token", "id_token", "api_key", "apikey"} {
		text = redactLooseKey(text, key)
	}
	text = redactAuthorizationBearer(text)
	return text
}

func redactLooseKey(text, key string) string {
	for {
		lower := strings.ToLower(text)
		idx := strings.Index(lower, strings.ToLower(key))
		if idx < 0 {
			return text
		}
		sepIdx := -1
		for i := idx + len(key); i < len(text); i++ {
			if text[i] == ' ' || text[i] == '\t' || text[i] == '"' || text[i] == '\'' {
				continue
			}
			if text[i] == ':' || text[i] == '=' || text[i] == '&' {
				sepIdx = i
			}
			break
		}
		if sepIdx < 0 {
			return text
		}
		start := sepIdx + 1
		for start < len(text) && (text[start] == ' ' || text[start] == '\t' || text[start] == '"' || text[start] == '\'') {
			start++
		}
		end := start
		for end < len(text) && text[end] != ',' && text[end] != '}' && text[end] != '"' && text[end] != '\'' && text[end] != '&' && text[end] != ';' {
			end++
		}
		if end <= start || strings.HasPrefix(text[start:], "[redacted]") {
			return text
		}
		text = text[:start] + "[redacted]" + text[end:]
	}
}

func redactAuthorizationBearer(text string) string {
	for {
		lower := strings.ToLower(text)
		idx := strings.Index(lower, "bearer ")
		if idx < 0 {
			return text
		}
		start := idx + len("bearer ")
		end := start
		for end < len(text) && text[end] != ' ' && text[end] != '\t' && text[end] != ',' && text[end] != ';' && text[end] != '"' && text[end] != '\'' {
			end++
		}
		if end <= start || strings.HasPrefix(text[start:], "[redacted]") {
			return text
		}
		text = text[:start] + "[redacted]" + text[end:]
	}
}

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
