package antigravity

import (
	"strings"
)

type ModelSpec struct {
	ID               string
	DisplayName      string
	UpstreamID       string
	LowUpstreamID    string
	MediumUpstreamID string
	HighUpstreamID   string
	Aliases          []string
}

var modelCatalog = []ModelSpec{
	{ID: "gemini-3-pro", DisplayName: "Gemini 3 Pro", UpstreamID: "gemini-3-pro", Aliases: []string{"gemini-3-pro"}},
	{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", UpstreamID: "gemini-3-flash"},
	{ID: "gemini-3-pro-high", DisplayName: "Gemini 3 Pro (High)", UpstreamID: "gemini-3-pro-high"},
	{ID: "gemini-3-pro-low", DisplayName: "Gemini 3 Pro (Low)", UpstreamID: "gemini-3-pro-low"},
	{
		ID:               "gemini-3.5-flash",
		DisplayName:      "Gemini 3.5 Flash",
		UpstreamID:       "gemini-3.5-flash-high",
		LowUpstreamID:    "gemini-3.5-flash-low",
		MediumUpstreamID: "gemini-3.5-flash-medium",
		HighUpstreamID:   "gemini-3.5-flash-high",
		Aliases:          []string{"gemini-3.5-flash-low", "gemini-3.5-flash-medium", "gemini-3.5-flash-high", "MODEL_PLACEHOLDER_M18"},
	},
	{
		ID:               "gemini-3.1-pro",
		DisplayName:      "Gemini 3.1 Pro",
		UpstreamID:       "gemini-3.1-pro-high",
		LowUpstreamID:    "gemini-3.1-pro-low",
		MediumUpstreamID: "gemini-pro-agent",
		HighUpstreamID:   "gemini-3.1-pro-high",
		Aliases:          []string{"gemini-pro-agent", "gemini-3.1-pro-low", "gemini-3.1-pro-high", "MODEL_PLACEHOLDER_M7", "MODEL_PLACEHOLDER_M8", "MODEL_PLACEHOLDER_M36", "MODEL_PLACEHOLDER_M37"},
	},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6 (Thinking)", UpstreamID: "claude-sonnet-4-6-thinking", HighUpstreamID: "claude-sonnet-4-6-thinking", Aliases: []string{"MODEL_PLACEHOLDER_M35"}},
	{ID: "claude-sonnet-4-6-thinking", DisplayName: "Claude Sonnet 4.6 (Thinking)", UpstreamID: "claude-sonnet-4-6-thinking"},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6 (Thinking)", UpstreamID: "claude-opus-4-6-thinking", HighUpstreamID: "claude-opus-4-6-thinking", Aliases: []string{"MODEL_PLACEHOLDER_M26"}},
	{ID: "claude-opus-4-6-thinking", DisplayName: "Claude Opus 4.6 (Thinking)", UpstreamID: "claude-opus-4-6-thinking"},
	{ID: "gpt-oss-120b", DisplayName: "GPT-OSS 120B", UpstreamID: "gpt-oss-120b", LowUpstreamID: "gpt-oss-120b", HighUpstreamID: "gpt-oss-120b-medium", Aliases: []string{"gpt-oss-120b-medium", "MODEL_OPENAI_GPT_OSS_120B_MEDIUM"}},
	{ID: "nano-banana-2", DisplayName: "Nano Banana 2", UpstreamID: "gemini-3.1-flash-image", Aliases: []string{"gemini-3.1-flash-image", "gemini-3-pro-image", "gemini-3-pro-image-preview"}},
}

var unavailableModelIDs = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

var visibleModelIDs = []string{
	"claude-opus-4-6",
	"claude-opus-4-6-thinking",
	"claude-sonnet-4-6",
	"claude-sonnet-4-6-thinking",
	"gemini-3-flash",
	"gemini-3-pro-high",
	"gemini-3-pro-image",
	"gemini-3-pro-image-preview",
	"gemini-3-pro-low",
	"gemini-3.1-flash-image",
	"gemini-3.1-pro",
	"gemini-3.1-pro-high",
	"gemini-3.1-pro-low",
	"gemini-3.5-flash",
	"gemini-3.5-flash-high",
	"gemini-3.5-flash-low",
	"gemini-3.5-flash-medium",
	"gemini-pro-agent",
	"gpt-oss-120b",
	"gpt-oss-120b-medium",
	"nano-banana-2",
	"gemini-3-pro",
}

var modelByAlias = buildModelAliasMap()

func UpstreamModelID(model string) string {
	if upstream := explicitTierUpstreamID(model); upstream != "" {
		return upstream
	}
	if spec, ok := CanonicalModel(model); ok {
		return spec.UpstreamID
	}
	return strings.TrimSpace(model)
}

func UpstreamModelIDForEffort(model, effort, requestSize string) string {
	return UpstreamModelIDForEffortWithThresholds(model, effort, requestSize, true)
}

func UpstreamModelIDForEffortWithThresholds(model, effort, requestSize string, autoTierRouting bool) string {
	return UpstreamModelIDForEffortWithSettings(model, effort, requestSize, ChannelSettings{ThinkingRouting: autoTierRouting, TierGroups: DefaultTierGroups()})
}

func UpstreamModelIDForEffortWithSettings(model, effort, requestSize string, settings ChannelSettings) string {
	if strings.TrimSpace(effort) == "" {
		if upstream := explicitTierUpstreamID(model); upstream != "" {
			return upstream
		}
	}
	if settings.ThinkingRouting {
		if group, ok := findTierGroup(model, settings.TierGroups); ok {
			if upstream := upstreamFromTierGroup(group, effort, requestSize); upstream != "" {
				return upstream
			}
		}
	}
	spec, ok := CanonicalModel(model)
	if !ok {
		return strings.TrimSpace(model)
	}
	if !settings.ThinkingRouting {
		if upstream := explicitTierUpstreamID(model); upstream != "" {
			return upstream
		}
		return strings.TrimSpace(model)
	}
	switch normalizeEffort(effort) {
	case "low", "minimal", "none":
		if spec.LowUpstreamID != "" {
			return spec.LowUpstreamID
		}
	case "high", "xhigh", "max":
		if spec.HighUpstreamID != "" {
			return spec.HighUpstreamID
		}
	case "medium":
		if spec.MediumUpstreamID != "" {
			return spec.MediumUpstreamID
		}
		if requestSize == "long" && spec.LowUpstreamID != "" {
			return spec.LowUpstreamID
		}
		if spec.HighUpstreamID != "" {
			return spec.HighUpstreamID
		}
	}
	switch requestSize {
	case "long":
		if spec.LowUpstreamID != "" {
			return spec.LowUpstreamID
		}
	case "medium":
		if spec.MediumUpstreamID != "" {
			return spec.MediumUpstreamID
		}
		if spec.HighUpstreamID != "" {
			return spec.HighUpstreamID
		}
	default:
		if spec.HighUpstreamID != "" {
			return spec.HighUpstreamID
		}
	}
	if spec.LowUpstreamID != "" {
		return spec.LowUpstreamID
	}
	return spec.UpstreamID
}

func FallbackUpstreamModels(model, currentUpstream string) []string {
	return FallbackUpstreamModelsWithSettings(model, currentUpstream, ChannelSettings{TierGroups: DefaultTierGroups()})
}

func FallbackUpstreamModelsWithSettings(model, currentUpstream string, settings ChannelSettings) []string {
	if group, ok := findTierGroup(model, settings.TierGroups); ok {
		return fallbackModelsFromTierGroup(group, currentUpstream)
	}
	spec, ok := CanonicalModel(model)
	if !ok {
		return nil
	}
	currentUpstream = strings.TrimPrefix(strings.TrimSpace(currentUpstream), "models/")
	tierOrder := []string{spec.HighUpstreamID, spec.MediumUpstreamID, spec.LowUpstreamID}
	currentTier := -1
	for i, tier := range tierOrder {
		if tier != "" && tier == currentUpstream {
			currentTier = i
			break
		}
	}
	if currentTier < 0 {
		for i, tier := range tierOrder {
			if tier != "" && tier == spec.UpstreamID {
				currentTier = i
				break
			}
		}
	}
	var candidates []string
	switch currentTier {
	case 0:
		candidates = []string{spec.MediumUpstreamID, spec.LowUpstreamID}
	case 1:
		candidates = []string{spec.HighUpstreamID, spec.LowUpstreamID}
	case 2:
		candidates = []string{spec.MediumUpstreamID, spec.HighUpstreamID}
	default:
		candidates = []string{spec.HighUpstreamID, spec.MediumUpstreamID, spec.LowUpstreamID}
	}
	seen := map[string]struct{}{currentUpstream: {}}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func DefaultTierGroups() []TierGroup {
	groups := make([]TierGroup, 0, len(modelCatalog))
	for _, spec := range modelCatalog {
		if spec.HighUpstreamID == "" && spec.MediumUpstreamID == "" && spec.LowUpstreamID == "" {
			continue
		}
		fallbackOrder := []string{"high", "medium", "low"}
		switch spec.ID {
		case "gemini-3.5-flash":
			fallbackOrder = []string{"medium", "low", "high"}
		case "gemini-3.1-pro":
			fallbackOrder = []string{"medium", "low", "high"}
		case "gpt-oss-120b":
			fallbackOrder = []string{"high", "low"}
		}
		groups = append(groups, TierGroup{
			PublicModel:   spec.ID,
			Aliases:       spec.Aliases,
			High:          spec.HighUpstreamID,
			Medium:        spec.MediumUpstreamID,
			Low:           spec.LowUpstreamID,
			FallbackOrder: fallbackOrder,
		})
	}
	return groups
}

func findTierGroup(model string, groups []TierGroup) (TierGroup, bool) {
	key := normalizeModelKey(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if key == "" {
		return TierGroup{}, false
	}
	for _, group := range groups {
		values := []string{group.PublicModel, group.High, group.Medium, group.Low}
		values = append(values, group.Aliases...)
		for _, value := range values {
			if normalizeModelKey(value) == key {
				return group, true
			}
		}
	}
	return TierGroup{}, false
}

func upstreamFromTierGroup(group TierGroup, effort, requestSize string) string {
	switch normalizeEffort(effort) {
	case "low", "minimal", "none":
		return firstNonEmpty(group.Low, group.Medium, group.High)
	case "high", "xhigh", "max":
		return firstNonEmpty(group.High, group.Medium, group.Low)
	case "medium":
		if group.Medium != "" {
			return group.Medium
		}
		if requestSize == "long" {
			return firstNonEmpty(group.Low, group.High)
		}
		return firstNonEmpty(group.High, group.Low)
	}
	switch requestSize {
	case "long":
		return firstNonEmpty(group.Low, group.Medium, group.High)
	case "medium":
		return firstNonEmpty(group.Medium, group.High, group.Low)
	default:
		return firstNonEmpty(group.High, group.Medium, group.Low)
	}
}

func fallbackModelsFromTierGroup(group TierGroup, currentUpstream string) []string {
	currentUpstream = strings.TrimPrefix(strings.TrimSpace(currentUpstream), "models/")
	tierValues := map[string]string{
		"high":   strings.TrimSpace(group.High),
		"medium": strings.TrimSpace(group.Medium),
		"low":    strings.TrimSpace(group.Low),
	}
	order := group.FallbackOrder
	if len(order) == 0 {
		order = []string{"high", "medium", "low"}
	}
	seen := map[string]struct{}{currentUpstream: {}}
	out := make([]string, 0, len(order))
	for _, value := range order {
		candidate := strings.TrimSpace(value)
		if _, isTier := tierValues[normalizeEffort(candidate)]; isTier {
			candidate = tierValues[normalizeEffort(candidate)]
		}
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
	out := make([]ModelSpec, 0, len(visibleModelIDs))
	for _, id := range visibleModelIDs {
		if spec, ok := CanonicalModel(id); ok {
			spec.ID = id
			spec.DisplayName = visibleDisplayName(id, spec.DisplayName)
			out = append(out, spec)
		}
	}
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
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if spec, ok := CanonicalModel(model); ok {
		return spec.DisplayName
	}
	return model
}

func PublicDisplayName(model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if display := visibleDisplayName(model, ""); display != "" {
		return display
	}
	return DisplayName(model)
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
		if IsHiddenModelID(model) {
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

func PublicModelForSettings(model string, settings ChannelSettings) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" || IsHiddenModelID(model) {
		return ""
	}
	if group, ok := findTierGroup(model, settings.TierGroups); ok && strings.TrimSpace(group.PublicModel) != "" {
		return strings.TrimSpace(group.PublicModel)
	}
	return ""
}

func PublicListForSettings(models []string, settings ChannelSettings) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models))
	for _, model := range models {
		public := PublicModelForSettings(model, settings)
		if public == "" {
			continue
		}
		if _, ok := seen[public]; ok {
			continue
		}
		seen[public] = struct{}{}
		out = append(out, public)
	}
	return out
}

func SupportsModelInList(model string, models []string, settings ChannelSettings) bool {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return false
	}
	for _, public := range PublicListForSettings(models, settings) {
		if public == model {
			return true
		}
	}
	if IsDirectUpstreamModelForSettings(model, settings) {
		return true
	}
	for _, raw := range models {
		if strings.TrimPrefix(strings.TrimSpace(raw), "models/") == model {
			return true
		}
	}
	return false
}

func ResolveRequestModelWithSettings(model, effort, requestSize string, settings ChannelSettings, availableModels []string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return ""
	}
	if IsTierEntryModelForSettings(model, settings) {
		return UpstreamModelIDForEffortWithSettings(model, effort, requestSize, settings)
	}
	if IsDirectUpstreamModelForSettings(model, settings) {
		return model
	}
	for _, available := range availableModels {
		if strings.TrimPrefix(strings.TrimSpace(available), "models/") == model {
			return model
		}
	}
	return model
}

func IsTierPublicModelForSettings(model string, settings ChannelSettings) bool {
	model = normalizeModelKey(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if model == "" {
		return false
	}
	for _, group := range settings.TierGroups {
		if normalizeModelKey(group.PublicModel) == model {
			return true
		}
	}
	return false
}

func IsTierEntryModelForSettings(model string, settings ChannelSettings) bool {
	model = normalizeModelKey(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if model == "" {
		return false
	}
	for _, group := range settings.TierGroups {
		values := []string{group.PublicModel}
		values = append(values, group.Aliases...)
		for _, value := range values {
			if normalizeModelKey(value) == model {
				return true
			}
		}
	}
	return false
}

func IsDirectUpstreamModelForSettings(model string, settings ChannelSettings) bool {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return false
	}
	for _, group := range settings.TierGroups {
		for _, upstream := range []string{group.High, group.Medium, group.Low} {
			if strings.TrimPrefix(strings.TrimSpace(upstream), "models/") == model {
				return true
			}
		}
	}
	return explicitTierUpstreamID(model) == model
}

func SupportsWithSettings(model string, settings ChannelSettings) bool {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return false
	}
	if _, ok := findTierGroup(model, settings.TierGroups); ok {
		return true
	}
	return Supports(model)
}

func Supports(model string) bool {
	_, ok := CanonicalModel(model)
	return ok
}

func IsHiddenModelID(model string) bool {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return true
	}
	if _, skip := unavailableModelIDs[model]; skip {
		return true
	}
	key := strings.ToLower(model)
	return strings.HasPrefix(key, "model_placeholder") || strings.HasPrefix(key, "chat_") || strings.HasPrefix(key, "tab_")
}

func visibleDisplayName(model, fallback string) string {
	switch normalizeModelKey(model) {
	case "gemini35flashmedium":
		return "Gemini 3.5 Flash (Medium)"
	case "gemini3flash", "gemini35flashhigh":
		return "Gemini 3.5 Flash (High)"
	case "gemini35flashlow":
		return "Gemini 3.5 Flash (Low)"
	case "gemini31prolow", "gemini3prolow":
		return "Gemini 3.1 Pro (Low)"
	case "gemini31prohigh", "gemini3prohigh":
		return "Gemini 3.1 Pro (High)"
	case "gptoss120bmedium":
		return "GPT-OSS 120B (Medium)"
	case "gemini31flashimage", "nanobanana2", "gemini3proimagepreview":
		return "Nano Banana 2"
	}
	return fallback
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

func normalizeEffort(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func explicitTierUpstreamID(model string) string {
	switch normalizeModelKey(strings.TrimPrefix(strings.TrimSpace(model), "models/")) {
	case "gemini35flashlow":
		return "gemini-3.5-flash-low"
	case "gemini35flashmedium":
		return "gemini-3.5-flash-medium"
	case "gemini35flashhigh":
		return "gemini-3.5-flash-high"
	case "gemini31prolow", "gemini3prolow", "modelplaceholderm7", "modelplaceholderm36":
		return "gemini-3.1-pro-low"
	case "gemini31prohigh", "gemini3prohigh", "modelplaceholderm8", "modelplaceholderm37":
		return "gemini-3.1-pro-high"
	case "gptoss120bmedium", "modelopenaigptoss120bmedium":
		return "gpt-oss-120b-medium"
	case "claudesonnet46thinking":
		return "claude-sonnet-4-6-thinking"
	case "claudeopus46thinking":
		return "claude-opus-4-6-thinking"
	case "nanobanana2", "gemini31flashimage":
		return "gemini-3.1-flash-image"
	case "gemini3proimage", "gemini3proimagepreview":
		return "gemini-3-pro-image"
	}
	return ""
}
