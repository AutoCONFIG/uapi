package antigravity

import (
	"sort"
	"strings"
)

var unavailableModelIDs = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

var upstreamModelAliases = map[string]string{
	"gemini-3.1-pro-high": "gemini-pro-agent",
}

func UpstreamModelID(model string) string {
	model = strings.TrimSpace(model)
	if alias, ok := upstreamModelAliases[model]; ok {
		return alias
	}
	return model
}

func NormalizeAvailableModels(models []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
		if model == "" {
			continue
		}
		model = UpstreamModelID(model)
		if _, skip := unavailableModelIDs[model]; skip {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
