package modelalias

import "strings"

type Mapping struct {
	UpstreamToPublic map[string]string
	PublicToUpstream map[string]string
}

func Parse(raw string) Mapping {
	m := Mapping{
		UpstreamToPublic: map[string]string{},
		PublicToUpstream: map[string]string{},
	}
	for _, entry := range splitEntries(raw) {
		public, upstream := splitPair(entry)
		if upstream == "" || public == "" {
			continue
		}
		if _, exists := m.PublicToUpstream[public]; exists {
			continue
		}
		if _, exists := m.UpstreamToPublic[upstream]; !exists {
			m.UpstreamToPublic[upstream] = public
		}
		m.PublicToUpstream[public] = upstream
	}
	return m
}

func PublicName(upstream, raw string) string {
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return ""
	}
	if public := Parse(raw).UpstreamToPublic[upstream]; public != "" {
		return public
	}
	return upstream
}

func UpstreamName(public, raw string) string {
	public = strings.TrimSpace(public)
	if public == "" {
		return ""
	}
	if upstream := Parse(raw).PublicToUpstream[public]; upstream != "" {
		return upstream
	}
	return public
}

func Supports(public, models, aliases string) bool {
	if strings.TrimSpace(models) == "" {
		return true
	}
	public = strings.TrimSpace(public)
	for _, model := range strings.Split(models, ",") {
		if strings.TrimSpace(model) == public {
			return true
		}
	}
	return false
}

func PublicList(models, aliases string) []string {
	modelSet := map[string]struct{}{}
	for _, model := range strings.Split(models, ",") {
		model = strings.TrimSpace(model)
		if model != "" {
			modelSet[model] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, entry := range splitEntries(aliases) {
		public, upstream := splitPair(entry)
		if upstream == "" || public == "" {
			continue
		}
		if _, ok := modelSet[public]; !ok {
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

func splitEntries(raw string) []string {
	raw = strings.NewReplacer("\r\n", "\n", ";", "\n").Replace(raw)
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	})
}

func splitPair(entry string) (string, string) {
	entry = strings.TrimSpace(entry)
	for _, sep := range []string{"=>", "=", ":"} {
		if idx := strings.Index(entry, sep); idx >= 0 {
			return strings.TrimSpace(entry[:idx]), strings.TrimSpace(entry[idx+len(sep):])
		}
	}
	return "", ""
}
