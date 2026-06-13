package quota

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/google/uuid"
)

type quotaDebugDump struct {
	Timestamp string                 `json:"timestamp"`
	Provider  string                 `json:"provider"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Upstream  interface{}            `json:"upstream,omitempty"`
	Quota     *QuotaData             `json:"quota,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

type quotaHTTPDebug struct {
	Request  quotaHTTPDebugRequest  `json:"request"`
	Response quotaHTTPDebugResponse `json:"response"`
}

type quotaHTTPDebugRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

type quotaHTTPDebugResponse struct {
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBytes  int                 `json:"body_bytes,omitempty"`
	Body       string              `json:"body,omitempty"`
}

func writeQuotaDebugDump(provider string, metadata map[string]interface{}, upstream interface{}, qd *QuotaData, err error) {
	baseDir := oauthdebug.DumpDir()
	if baseDir == "" {
		return
	}
	now := time.Now()
	name := now.Local().Format("20060102T150405.000000000-0700") + "-quota-" + provider + "-" + uuid.NewString()
	outDir := filepath.Join(filepath.Clean(baseDir), now.Local().Format("2006-01-02"), "quota", debugdump.SafeName(provider), name)
	if mkErr := os.MkdirAll(outDir, 0755); mkErr != nil {
		logger.Warnf("quota.debug_dump", "create quota dump dir failed", logger.Err(mkErr), logger.F("dir", outDir))
		return
	}
	record := quotaDebugDump{
		Timestamp: now.Local().Format(time.RFC3339Nano),
		Provider:  provider,
		Metadata:  oauthdebug.RedactMap(metadata),
		Upstream:  upstream,
		Quota:     qd,
	}
	if err != nil {
		record.Error = oauthdebug.RedactText(err.Error())
	}
	raw, marshalErr := json.MarshalIndent(record, "", "  ")
	if marshalErr != nil {
		logger.Warnf("quota.debug_dump", "marshal quota dump failed", logger.Err(marshalErr), logger.F("dir", outDir))
		return
	}
	if writeErr := os.WriteFile(filepath.Join(outDir, "quota.json"), raw, 0644); writeErr != nil {
		logger.Warnf("quota.debug_dump", "write quota dump failed", logger.Err(writeErr), logger.F("dir", outDir))
	}
	debugdump.AppendIndex(now, debugdump.Entry{
		Side:     "relay",
		Category: "quota",
		Span:     "quota.fetch",
		Status:   quotaDebugStatus(err),
		Error:    record.Error,
		DumpPath: filepath.ToSlash(strings.TrimPrefix(outDir, filepath.Clean(baseDir)+string(os.PathSeparator))),
		Extra: map[string]interface{}{
			"provider": provider,
		},
	})
}

func quotaDebugStatus(err error) int {
	if err != nil {
		return 500
	}
	return 200
}

func newQuotaHTTPDebug(req *http.Request, requestBody []byte) *quotaHTTPDebug {
	if req == nil {
		return nil
	}
	debugInfo := &quotaHTTPDebug{
		Request: quotaHTTPDebugRequest{
			Method:  req.Method,
			URL:     oauthdebug.RedactURL(req.URL.String()),
			Headers: oauthdebug.RedactRequestHeaders(req.Header),
		},
	}
	if len(requestBody) > 0 {
		debugInfo.Request.Body = quotaDebugBodyPreview(requestBody, 10000)
	}
	return debugInfo
}

func finishQuotaHTTPDebug(debugInfo *quotaHTTPDebug, resp *http.Response, responseBody []byte) {
	if debugInfo == nil || resp == nil {
		return
	}
	debugInfo.Response.StatusCode = resp.StatusCode
	debugInfo.Response.Headers = oauthdebug.RedactResponseHeaders(resp.Header)
	debugInfo.Response.BodyBytes = len(responseBody)
	debugInfo.Response.Body = quotaDebugBodyPreview(responseBody, 10000)
}

func redactQuotaDebugMetadata(metadata map[string]interface{}) map[string]interface{} {
	return oauthdebug.RedactMap(metadata)
}

func redactedQuotaRequestHeaders(headers http.Header) map[string]string {
	return oauthdebug.RedactRequestHeaders(headers)
}

func redactedQuotaResponseHeaders(headers http.Header) map[string][]string {
	return oauthdebug.RedactResponseHeaders(headers)
}

func isQuotaSecretHeader(key string) bool {
	return oauthdebug.IsSecretHeader(key)
}

func quotaDebugBodyPreview(body []byte, limit int) string {
	return oauthdebug.BodyPreview(body, limit)
}
