package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/google/uuid"
)

const relayDebugDumpDirEnv = "UAPI_RELAY_DEBUG_DUMP_DIR"

var relayDebugDumpDir = os.Getenv(relayDebugDumpDirEnv)

func relayDebugDumpEnabled() bool {
	return relayDebugDumpDir != ""
}

type relayDebugDumpSummary struct {
	Timestamp      string          `json:"timestamp"`
	TokenID        string          `json:"token_id,omitempty"`
	AccountID      string          `json:"account_id,omitempty"`
	ChannelID      string          `json:"channel_id,omitempty"`
	GatewayRequest string          `json:"gateway_request,omitempty"`
	ClientFormat   provider.Format `json:"client_format"`
	UpstreamFormat provider.Format `json:"upstream_format"`
	RequestType    string          `json:"request_type"`
	Model          string          `json:"model"`
	RoutedModel    string          `json:"routed_model"`
	Stream         bool            `json:"stream"`
	OriginalBytes  int             `json:"original_bytes"`
	ConvertedBytes int             `json:"converted_bytes"`
}

func dumpRelayRequestDebug(original, converted []byte, token db.Token, ch *db.Channel, acc *db.Account, claims *internalauth.Claims, clientFormat, upstreamFormat provider.Format, requestType relayRequestType, model, routedModel string, stream bool) {
	if relayDebugDumpDir == "" {
		return
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + uuid.NewString()
	outDir := filepath.Join(relayDebugDumpDir, name)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logger.Warnf("relay.debug_dump", "create dump dir failed", logger.Err(err), logger.F("dir", outDir))
		return
	}

	summary := relayDebugDumpSummary{
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		TokenID:        token.ID.String(),
		ClientFormat:   clientFormat,
		UpstreamFormat: upstreamFormat,
		RequestType:    string(requestType),
		Model:          model,
		RoutedModel:    routedModel,
		Stream:         stream,
		OriginalBytes:  len(original),
		ConvertedBytes: len(converted),
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
	logger.Debugf("relay.debug_dump", "request dump written",
		logger.F("dump_dir", outDir),
		logger.F("client_format", string(clientFormat)),
		logger.F("upstream_format", string(upstreamFormat)),
		logger.F("original_bytes", len(original)),
		logger.F("converted_bytes", len(converted)),
	)
}

func writeRelayDebugFile(dir, name string, body []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
		logger.Warnf("relay.debug_dump", "write dump file failed", logger.Err(err), logger.F("file", name))
	}
}
