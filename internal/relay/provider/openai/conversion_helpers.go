package openai

import (
	"strings"

	"github.com/AutoCONFIG/uapi/internal/logger"
)

func warnSkippedFields(source, target string, fields []string) {
	if len(fields) == 0 {
		return
	}
	logger.Component("provider.openai").Warn("cross-protocol conversion: fields skipped as no equivalent in target",
		logger.F("source_format", source),
		logger.F("target_format", target),
		logger.F("skipped_fields", strings.Join(fields, ",")),
		logger.F("reason", "no equivalent field in target protocol"))
}
