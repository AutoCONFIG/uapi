package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/google/uuid"
)

const quotaDebugDumpDirEnv = "UAPI_RELAY_DEBUG_DUMP_DIR"

type quotaDebugDump struct {
	Timestamp string                 `json:"timestamp"`
	Provider  string                 `json:"provider"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Upstream  interface{}            `json:"upstream,omitempty"`
	Quota     *QuotaData             `json:"quota,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

func writeQuotaDebugDump(provider string, metadata map[string]interface{}, upstream interface{}, qd *QuotaData, err error) {
	baseDir := strings.TrimSpace(os.Getenv(quotaDebugDumpDirEnv))
	if baseDir == "" {
		return
	}
	now := time.Now()
	dayDir := filepath.Join(filepath.Clean(baseDir), now.Local().Format("2006-01-02"))
	name := now.Local().Format("20060102T150405.000000000-0700") + "-quota-" + provider + "-" + uuid.NewString()
	outDir := filepath.Join(dayDir, name)
	if mkErr := os.MkdirAll(outDir, 0755); mkErr != nil {
		logger.Warnf("quota.debug_dump", "create quota dump dir failed", logger.Err(mkErr), logger.F("dir", outDir))
		return
	}
	record := quotaDebugDump{
		Timestamp: now.Local().Format(time.RFC3339Nano),
		Provider:  provider,
		Metadata:  redactQuotaDebugMetadata(metadata),
		Upstream:  upstream,
		Quota:     qd,
	}
	if err != nil {
		record.Error = logger.Redact(err.Error())
	}
	raw, marshalErr := json.MarshalIndent(record, "", "  ")
	if marshalErr != nil {
		logger.Warnf("quota.debug_dump", "marshal quota dump failed", logger.Err(marshalErr), logger.F("dir", outDir))
		return
	}
	if writeErr := os.WriteFile(filepath.Join(outDir, "quota.json"), raw, 0644); writeErr != nil {
		logger.Warnf("quota.debug_dump", "write quota dump failed", logger.Err(writeErr), logger.F("dir", outDir))
	}
}

func redactQuotaDebugMetadata(metadata map[string]interface{}) map[string]interface{} {
	if metadata == nil {
		return nil
	}
	out := make(map[string]interface{}, len(metadata))
	for key, value := range metadata {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") {
			out[key] = "[redacted]"
			continue
		}
		if text, ok := value.(string); ok {
			out[key] = logger.Redact(text)
			continue
		}
		out[key] = value
	}
	return out
}
