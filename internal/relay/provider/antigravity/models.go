package antigravity

import (
	"strings"
)

type ModelSpec struct {
	ID          string
	DisplayName string
	UpstreamID  string
	Aliases     []string
}

var modelCatalog = []ModelSpec{
	{ID: "gemini-3.5-flash-medium", DisplayName: "Gemini 3.5 Flash (Medium)", UpstreamID: "gemini-3-flash-agent", Aliases: []string{"gemini-3-flash-agent", "gemini-3.5-flash"}},
	{ID: "gemini-3.5-flash-high", DisplayName: "Gemini 3.5 Flash (High)", UpstreamID: "gemini-3-flash-agent"},
	{ID: "gemini-3.5-flash-low", DisplayName: "Gemini 3.5 Flash (Low)", UpstreamID: "gemini-3.5-flash-low"},
	{ID: "gemini-3.1-pro-low", DisplayName: "Gemini 3.1 Pro (Low)", UpstreamID: "gemini-3.1-pro-low", Aliases: []string{"gemini-3-pro-low", "MODEL_PLACEHOLDER_M7", "MODEL_PLACEHOLDER_M36"}},
	{ID: "gemini-3.1-pro-high", DisplayName: "Gemini 3.1 Pro (High)", UpstreamID: "gemini-pro-agent", Aliases: []string{"gemini-pro-agent", "gemini-3-pro-high", "MODEL_PLACEHOLDER_M8", "MODEL_PLACEHOLDER_M37"}},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6 (Thinking)", UpstreamID: "claude-sonnet-4-6", Aliases: []string{"claude-sonnet-4-6-thinking", "MODEL_PLACEHOLDER_M35"}},
	{ID: "claude-opus-4-6-thinking", DisplayName: "Claude Opus 4.6 (Thinking)", UpstreamID: "claude-opus-4-6-thinking", Aliases: []string{"claude-opus-4-6", "MODEL_PLACEHOLDER_M26"}},
	{ID: "gpt-oss-120b-medium", DisplayName: "GPT-OSS 120B (Medium)", UpstreamID: "gpt-oss-120b-medium", Aliases: []string{"MODEL_OPENAI_GPT_OSS_120B_MEDIUM"}},
	{ID: "nano-banana-2", DisplayName: "Nano Banana 2", UpstreamID: "gemini-3.1-flash-image", Aliases: []string{"gpt-image-1", "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image", "gemini-3-pro-image-preview"}},
}

var unavailableModelIDs = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

var modelByAlias = buildModelAliasMap()

func UpstreamModelID(model string) string {
	if spec, ok := CanonicalModel(model); ok {
		return spec.UpstreamID
	}
	return strings.TrimSpace(model)
}

func PublicModelCSV() string {
	models := PublicModels()
	ids := make([]string, 0, len(models))
	for _, spec := range models {
		ids = append(ids, spec.ID)
	}
	return strings.Join(ids, ",")
}

func PublicModels() []ModelSpec {
	out := make([]ModelSpec, len(modelCatalog))
	copy(out, modelCatalog)
	return out
}

func CanonicalModel(model string) (ModelSpec, bool) {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return ModelSpec{}, false
	}
	spec, ok := modelByAlias[normalizeModelKey(model)]
	return spec, ok
}

func DisplayName(model string) string {
	if spec, ok := CanonicalModel(model); ok {
		return spec.DisplayName
	}
	return strings.TrimPrefix(strings.TrimSpace(model), "models/")
}

func IsImageToolModel(model string) bool {
	spec, ok := CanonicalModel(model)
	return ok && strings.Contains(strings.ToLower(spec.UpstreamID), "image")
}

func NormalizeAvailableModels(models []string) []string {
	seen := map[string]struct{}{}
	available := map[string]struct{}{}
	for _, model := range models {
		model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
		if model == "" {
			continue
		}
		if _, skip := unavailableModelIDs[model]; skip {
			continue
		}
		available[normalizeModelKey(model)] = struct{}{}
	}
	out := make([]string, 0, len(available))
	for _, spec := range PublicModels() {
		if !modelSpecAvailable(spec, available) {
			continue
		}
		if _, ok := seen[spec.ID]; ok {
			continue
		}
		seen[spec.ID] = struct{}{}
		out = append(out, spec.ID)
	}
	return out
}

func modelSpecAvailable(spec ModelSpec, available map[string]struct{}) bool {
	values := []string{spec.ID, spec.DisplayName, spec.UpstreamID}
	values = append(values, spec.Aliases...)
	for _, value := range values {
		if _, ok := available[normalizeModelKey(value)]; ok {
			return true
		}
	}
	return false
}

func buildModelAliasMap() map[string]ModelSpec {
	out := map[string]ModelSpec{}
	for _, spec := range modelCatalog {
		values := []string{spec.ID, spec.DisplayName, spec.UpstreamID}
		values = append(values, spec.Aliases...)
		for _, value := range values {
			key := normalizeModelKey(value)
			if key == "" {
				continue
			}
			if _, exists := out[key]; !exists {
				out[key] = spec
			}
		}
	}
	return out
}

func normalizeModelKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
