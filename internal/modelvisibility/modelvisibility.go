package modelvisibility

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/httputil"
	"github.com/AutoCONFIG/uapi/internal/modelalias"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"gorm.io/gorm"
)

func PublicModelSet(database *gorm.DB) (map[string]struct{}, error) {
	var rows []struct {
		Models       string
		ModelAliases string
		APIFormat    string
		Settings     string
	}
	if err := database.Table("channels").
		Select("DISTINCT channels.models, channels.model_aliases, channels.api_format, channels.settings").
		Joins("JOIN accounts ON accounts.channel_id = channels.id AND accounts.enabled = true AND accounts.deleted_at IS NULL").
		Where("channels.enabled = true AND channels.deleted_at IS NULL AND channels.models <> ''").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, row := range rows {
		for _, model := range PublicModelsForChannel(row.Models, row.ModelAliases, row.APIFormat, row.Settings) {
			out[model] = struct{}{}
		}
	}
	return out, nil
}

func PublicModelsForChannel(models, aliases, apiFormat, settingsRaw string) []string {
	if apiFormat != "antigravity" {
		return modelalias.PublicList(models, aliases)
	}
	settings := antigravity.ParseChannelSettings(settingsRaw)
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, public := range antigravity.PublicListForSettings(httputil.CSVList(models), settings) {
		if _, ok := seen[public]; ok {
			continue
		}
		seen[public] = struct{}{}
		out = append(out, public)
	}
	return out
}

func FilterCSV(raw string, visible map[string]struct{}) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, model := range strings.Split(raw, ",") {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := visible[model]; !ok {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return strings.Join(out, ",")
}

func FilterRatios(raw string, visible map[string]struct{}) string {
	var ratios map[string]int
	if err := json.Unmarshal([]byte(raw), &ratios); err != nil || len(ratios) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(ratios))
	for model := range ratios {
		if _, ok := visible[model]; ok {
			keys = append(keys, model)
		}
	}
	sort.Strings(keys)
	out := make(map[string]int, len(keys))
	for _, model := range keys {
		out[model] = ratios[model]
	}
	if len(out) == 0 {
		return "{}"
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func FilterRatioItems(raw string, visible map[string]struct{}) []struct {
	Model string
	Ratio int
} {
	var ratios map[string]int
	if err := json.Unmarshal([]byte(raw), &ratios); err != nil || len(ratios) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ratios))
	for model := range ratios {
		if _, ok := visible[model]; ok {
			keys = append(keys, model)
		}
	}
	sort.Strings(keys)
	out := make([]struct {
		Model string
		Ratio int
	}, 0, len(keys))
	for _, model := range keys {
		out = append(out, struct {
			Model string
			Ratio int
		}{Model: model, Ratio: ratios[model]})
	}
	return out
}
