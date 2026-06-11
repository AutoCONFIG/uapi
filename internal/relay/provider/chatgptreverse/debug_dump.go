package chatgptreverse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/google/uuid"
)

const chatGPTReverseDebugDumpDirEnv = "UAPI_RELAY_DEBUG_DUMP_DIR"

const (
	chatGPTReverseDebugTextPreviewLimit = 4000
	chatGPTReverseDebugJSONTextLimit    = 4096
)

type chatGPTReverseDebugTrace struct {
	ID  string
	Dir string
	mu  sync.Mutex
	seq int
}

type chatGPTReverseDebugSummary struct {
	Timestamp   string `json:"timestamp"`
	TraceID     string `json:"trace_id"`
	ProcessID   int    `json:"process_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	APIFormat   string `json:"api_format,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	AccountType string `json:"account_type,omitempty"`
}

type chatGPTReverseHTTPDebug struct {
	Timestamp  string                          `json:"timestamp"`
	TraceID    string                          `json:"trace_id"`
	Seq        int                             `json:"seq"`
	DurationMS int64                           `json:"duration_ms"`
	Request    chatGPTReverseHTTPDebugRequest  `json:"request"`
	Response   chatGPTReverseHTTPDebugResponse `json:"response,omitempty"`
	Error      string                          `json:"error,omitempty"`
}

type chatGPTReverseHTTPDebugRequest struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Path      string            `json:"path,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	BodyBytes int               `json:"body_bytes,omitempty"`
	Body      interface{}       `json:"body,omitempty"`
}

type chatGPTReverseHTTPDebugResponse struct {
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBytes  int                 `json:"body_bytes,omitempty"`
	Body       interface{}         `json:"body,omitempty"`
	Streaming  bool                `json:"streaming,omitempty"`
}

func newChatGPTReverseDebugDump(ch *db.Channel, acc *db.Account) *chatGPTReverseDebugTrace {
	baseDir := strings.TrimSpace(os.Getenv(chatGPTReverseDebugDumpDirEnv))
	if baseDir == "" {
		return nil
	}
	now := time.Now()
	traceID := uuid.NewString()
	dayDir := filepath.Join(filepath.Clean(baseDir), now.Local().Format("2006-01-02"))
	name := now.Local().Format("20060102T150405.000000000-0700") + "-chatgpt_reverse-" + traceID
	outDir := filepath.Join(dayDir, name)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logger.Warnf("chatgpt_reverse.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", outDir))
		return nil
	}
	summary := chatGPTReverseDebugSummary{
		Timestamp: now.Local().Format(time.RFC3339Nano),
		TraceID:   traceID,
		ProcessID: os.Getpid(),
	}
	if ch != nil {
		summary.ChannelID = ch.ID.String()
		summary.ChannelName = ch.Name
		summary.ChannelType = ch.Type
		summary.APIFormat = ch.APIFormat
	}
	if acc != nil {
		summary.AccountID = acc.ID.String()
		summary.AccountName = acc.Name
		summary.AccountType = acc.CredType
	}
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		writeChatGPTReverseDebugFile(outDir, "summary.json", raw)
	}
	return &chatGPTReverseDebugTrace{ID: traceID, Dir: outDir}
}

func (t *chatGPTReverseDebugTrace) HTTP(method, rawURL string, headers map[string]string, requestBody []byte, statusCode int, responseHeaders http.Header, responseBody []byte, streaming bool, started time.Time, err error) {
	if t == nil {
		return
	}
	record := chatGPTReverseHTTPDebug{
		Timestamp:  time.Now().Local().Format(time.RFC3339Nano),
		TraceID:    t.ID,
		DurationMS: time.Since(started).Milliseconds(),
		Request: chatGPTReverseHTTPDebugRequest{
			Method:    method,
			URL:       redactChatGPTReverseURL(rawURL),
			Path:      chatGPTReverseURLPath(rawURL),
			Headers:   chatGPTReverseDebugRequestHeaders(headers),
			BodyBytes: len(requestBody),
			Body:      chatGPTReverseDebugBody(method, rawURL, requestBody),
		},
		Response: chatGPTReverseHTTPDebugResponse{
			StatusCode: statusCode,
			Headers:    chatGPTReverseDebugResponseHeaders(responseHeaders),
			BodyBytes:  len(responseBody),
			Body:       chatGPTReverseDebugBody("RESPONSE", rawURL, responseBody),
			Streaming:  streaming,
		},
	}
	if err != nil {
		record.Error = oauthdebug.RedactText(err.Error())
	}
	raw, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		logger.Warnf("chatgpt_reverse.debug_dump", "marshal http dump failed", logger.Err(marshalErr), logger.F("trace_id", t.ID))
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	record.Seq = t.seq
	raw, _ = json.Marshal(record)
	writeChatGPTReverseDebugJSONL(t.Dir, "http.jsonl", raw)
}

func chatGPTReverseDebugRequestHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range headers {
		if !isChatGPTReverseDebugRequestHeader(key) {
			continue
		}
		if oauthdebug.IsSecretHeader(key) || isChatGPTReverseSecretKey(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = oauthdebug.RedactText(value)
	}
	return out
}

func chatGPTReverseDebugResponseHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := map[string][]string{}
	for key, values := range headers {
		if !isChatGPTReverseDebugResponseHeader(key) {
			continue
		}
		if oauthdebug.IsSecretHeader(key) || isChatGPTReverseSecretKey(key) {
			out[key] = []string{"[redacted]"}
			continue
		}
		copied := make([]string, len(values))
		for i, value := range values {
			copied[i] = oauthdebug.RedactText(value)
		}
		out[key] = copied
	}
	return out
}

func redactChatGPTReverseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return oauthdebug.RedactText(raw)
	}
	if strings.Contains(strings.ToLower(parsed.Host), "blob.core.windows.net") {
		return parsed.Scheme + "://" + parsed.Host + parsed.Path + "?[signed-query-redacted]"
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if isChatGPTReverseSecretKey(key) {
			query.Set(key, "[redacted]")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return oauthdebug.RedactText(parsed.String())
}

func chatGPTReverseURLPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Path
}

func chatGPTReverseDebugBody(method, rawURL string, body []byte) interface{} {
	if len(body) == 0 {
		return nil
	}
	if method == http.MethodPut {
		sum := sha256.Sum256(body)
		return map[string]interface{}{
			"omitted": true,
			"reason":  "raw upload bytes",
			"sha256":  hex.EncodeToString(sum[:]),
		}
	}
	var decoded interface{}
	if err := json.Unmarshal(body, &decoded); err == nil {
		return redactChatGPTReverseValue(decoded)
	}
	return oauthdebug.BodyPreview(body, chatGPTReverseDebugTextPreviewLimit)
}

func redactChatGPTReverseValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, child := range v {
			if isChatGPTReverseSecretKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactChatGPTReverseValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, child := range v {
			out[i] = redactChatGPTReverseValue(child)
		}
		return out
	case string:
		return redactChatGPTReverseString(v)
	default:
		return value
	}
}

func redactChatGPTReverseString(value string) string {
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return redactChatGPTReverseURL(value)
	}
	redacted := oauthdebug.RedactText(value)
	if len(redacted) > chatGPTReverseDebugJSONTextLimit {
		return redacted[:chatGPTReverseDebugJSONTextLimit] + "..."
	}
	return redacted
}

func isChatGPTReverseSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if oauthdebug.IsSecretKey(normalized) {
		return true
	}
	switch normalized {
	case "authorization", "cookie", "set_cookie", "upload_url", "conduit_token", "openai_sentinel_chat_requirements_token", "openai_sentinel_proof_token", "openai_sentinel_so_token", "openai_sentinel_turnstile_token", "sig", "signature", "se", "sp", "sv", "sr":
		return true
	}
	return strings.Contains(normalized, "token") || strings.Contains(normalized, "cookie")
}

func isChatGPTReverseDebugRequestHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "accept",
		"authorization",
		"content-type",
		"openai-sentinel-chat-requirements-token",
		"openai-sentinel-proof-token",
		"openai-sentinel-so-token",
		"openai-sentinel-turnstile-token",
		"origin",
		"referer",
		"x-conduit-token",
		"x-ms-blob-type",
		"x-ms-version",
		"x-oai-turn-trace-id",
		"x-openai-target-path",
		"x-openai-target-route":
		return true
	default:
		return false
	}
}

func isChatGPTReverseDebugResponseHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "content-type", "content-length", "location", "x-request-id", "openai-processing-ms":
		return true
	default:
		return false
	}
}

func writeChatGPTReverseDebugFile(dir, name string, body []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
		logger.Warnf("chatgpt_reverse.debug_dump", "write dump file failed", logger.Err(err), logger.F("file", filepath.Join(dir, name)))
	}
}

func writeChatGPTReverseDebugJSONL(dir, name string, raw []byte) {
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warnf("chatgpt_reverse.debug_dump", "open jsonl failed", logger.Err(err), logger.F("file", filepath.Join(dir, name)))
		return
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		logger.Warnf("chatgpt_reverse.debug_dump", "write jsonl failed", logger.Err(err), logger.F("file", filepath.Join(dir, name)))
	}
}
