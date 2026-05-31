package quota

import (
	"fmt"
	"sort"
	"strings"
	"time"

	openai "github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

func init() {
	Register("codex", &codexFetcher{})
}

type codexFetcher struct{}

func (c *codexFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	accountID := codexAccountID(metadata)
	fedramp := codexFedramp(metadata)

	usage, err := openai.FetchCodexUsage(accessToken, accountID, fedramp)
	if err != nil {
		if strings.Contains(err.Error(), "status 403") {
			return &QuotaData{
				IsForbidden:     true,
				ForbiddenReason: "account_forbidden",
			}, nil
		}
		return nil, err
	}
	return convertCodexUsage(usage), nil
}

func codexAccountID(metadata map[string]interface{}) string {
	if id, ok := metadata["chatgpt_account_id"].(string); ok {
		return id
	}
	return ""
}

func codexFedramp(metadata map[string]interface{}) bool {
	if v, ok := metadata["chatgpt_account_is_fedramp"].(bool); ok {
		return v
	}
	return false
}

func convertCodexUsage(raw map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	limits := raw
	if rl, ok := raw["rate_limit"].(map[string]interface{}); ok {
		limits = rl
	} else if rl, ok := raw["rate_limits"].(map[string]interface{}); ok {
		limits = rl
	} else if rl, ok := raw["rateLimits"].(map[string]interface{}); ok {
		limits = rl
	}

	addCodexWindow(qd, codexWindowDisplayLabel(mapValue(limits, "primary_window"), "Codex 5小时窗口"), mapValue(limits, "primary_window"))
	addCodexWindow(qd, codexWindowDisplayLabel(mapValue(limits, "secondary_window"), "Codex 每周窗口"), mapValue(limits, "secondary_window"))
	addCodexWindow(qd, codexWindowDisplayLabel(mapValue(limits, "primary"), "Codex 5小时窗口"), mapValue(limits, "primary"))
	addCodexWindow(qd, codexWindowDisplayLabel(mapValue(limits, "secondary"), "Codex 每周窗口"), mapValue(limits, "secondary"))
	if len(qd.Buckets) == 0 {
		collectCodexWindows(qd, "", limits)
		normalizeCodexWindowLabels(qd)
	}

	if credits, ok := limits["credits"].(map[string]interface{}); ok {
		hasCredits, _ := credits["has_credits"].(bool)
		if hasCredits {
			unlimited, _ := credits["unlimited"].(bool)
			balance, _ := credits["balance"].(string)
			qd.Credits = &CreditsInfo{
				Balance:   balance,
				Unlimited: unlimited,
				Label:     "Codex Credits",
			}
		}
	}

	if planType := firstString(raw, "plan_type", "planType", "tier", "account_plan", "accountPlan"); planType != "" {
		qd.Tier = planType
	}

	return qd
}

func addCodexWindow(qd *QuotaData, label string, window map[string]interface{}) {
	if window == nil {
		return
	}
	if remainingPct := codexRemainingPercent(window); remainingPct != nil {
		if quotaBucketExists(qd, label) {
			return
		}
		usedPercent := clampPercent(100 - *remainingPct)
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            label,
			RemainingPercent: clampPercent(*remainingPct),
			UsedPercent:      &usedPercent,
			ResetTime:        codexResetTime(window),
		})
	}
}

func collectCodexWindows(qd *QuotaData, prefix string, m map[string]interface{}) {
	if m == nil {
		return
	}
	if remainingPct := codexRemainingPercent(m); remainingPct != nil {
		label := firstString(m, "label", "name", "model", "model_id", "modelId", "window")
		if label == "" {
			label = prefix
		}
		label = codexWindowLabel(label)
		if label == "" {
			label = "Codex 额度"
		}
		key := "Codex " + label
		if !quotaBucketExists(qd, key) {
			usedPercent := clampPercent(100 - *remainingPct)
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            key,
				RemainingPercent: clampPercent(*remainingPct),
				UsedPercent:      &usedPercent,
				ResetTime:        codexResetTime(m),
			})
		}
	}
	for key, value := range m {
		child, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if key == "credits" {
			continue
		}
		childPrefix := strings.TrimSpace(key)
		if prefix != "" {
			childPrefix = prefix + " " + childPrefix
		}
		collectCodexWindows(qd, childPrefix, child)
	}
}

func normalizeCodexWindowLabels(qd *QuotaData) {
	if len(qd.Buckets) != 2 {
		return
	}
	for _, bucket := range qd.Buckets {
		if bucket.Label != "Codex rate_limit" && bucket.Label != "Codex 额度" {
			return
		}
	}
	sort.SliceStable(qd.Buckets, func(i, j int) bool {
		return resetUnix(qd.Buckets[i].ResetTime) < resetUnix(qd.Buckets[j].ResetTime)
	})
	qd.Buckets[0].Label = "Codex 5小时窗口"
	qd.Buckets[1].Label = "Codex 每周窗口"
}

func codexWindowLabel(label string) string {
	lower := strings.ToLower(strings.TrimSpace(label))
	switch {
	case strings.Contains(lower, "weekly"), strings.Contains(lower, "week"):
		return "每周窗口"
	case strings.Contains(lower, "5h"), strings.Contains(lower, "five"):
		return "5小时窗口"
	case strings.Contains(lower, "primary_window"), strings.Contains(lower, "primary"):
		return "5小时窗口"
	case strings.Contains(lower, "secondary_window"), strings.Contains(lower, "secondary"):
		return "每周窗口"
	default:
		return strings.TrimSpace(label)
	}
}

func codexWindowDisplayLabel(window map[string]interface{}, fallback string) string {
	if window == nil {
		return fallback
	}
	if label := codexWindowDurationLabel(window); label != "" {
		return "Codex " + label
	}
	return fallback
}

func codexWindowDurationLabel(window map[string]interface{}) string {
	seconds := firstFloat(window, "limit_window_seconds", "limitWindowSeconds", "window_seconds", "windowSeconds")
	if seconds == nil || *seconds <= 0 {
		return ""
	}
	minutes := int64((*seconds / 60) + 0.5)
	switch {
	case approximateMinutes(minutes, 5*60):
		return "5小时窗口"
	case approximateMinutes(minutes, 24*60):
		return "每日窗口"
	case approximateMinutes(minutes, 7*24*60):
		return "每周窗口"
	case approximateMinutes(minutes, 30*24*60):
		return "每月窗口"
	case approximateMinutes(minutes, 365*24*60):
		return "每年窗口"
	default:
		return ""
	}
}

func approximateMinutes(value, expected int64) bool {
	if value < 0 {
		value = 0
	}
	min := float64(expected) * 0.95
	max := float64(expected) * 1.05
	actual := float64(value)
	return actual >= min && actual <= max
}

func resetUnix(value string) int64 {
	if value == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Unix()
	}
	var seconds int64
	if _, err := fmt.Sscanf(value, "%d", &seconds); err == nil {
		return seconds
	}
	return 0
}

func quotaBucketExists(qd *QuotaData, label string) bool {
	for _, bucket := range qd.Buckets {
		if bucket.Label == label {
			return true
		}
	}
	return false
}

func codexRemainingPercent(m map[string]interface{}) *int {
	if v := firstFloat(m, "remaining_percent", "remainingPercent", "remaining_percentage", "remainingPercentage"); v != nil {
		pct := int(*v)
		return &pct
	}
	if v := firstFloat(m, "remaining_fraction", "remainingFraction"); v != nil {
		pct := int(*v * 100)
		return &pct
	}
	if v := firstFloat(m, "used_percent", "usedPercent", "used_percentage", "usedPercentage", "utilization"); v != nil {
		used := *v
		if used <= 1 {
			used *= 100
		}
		pct := 100 - int(used)
		return &pct
	}
	limit := firstFloat(m, "limit", "total", "quota", "max")
	used := firstFloat(m, "used", "usage", "consumed")
	if limit != nil && used != nil && *limit > 0 {
		pct := int(((*limit - *used) / *limit) * 100)
		return &pct
	}
	return nil
}

func codexResetTime(m map[string]interface{}) string {
	for _, key := range []string{"resets_at", "reset_at", "resetAt"} {
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	for _, key := range []string{"resets_at", "reset_at", "resetAt"} {
		if v := firstFloat(m, key); v != nil {
			seconds := int64(*v)
			if seconds > 0 {
				return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
			}
			return fmt.Sprintf("%d", seconds)
		}
	}
	if v := firstFloat(m, "reset_after_seconds", "resetAfterSeconds"); v != nil && *v > 0 {
		return time.Now().UTC().Add(time.Duration(*v) * time.Second).Format(time.RFC3339)
	}
	return ""
}

func mapValue(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if value, ok := m[key].(map[string]interface{}); ok {
		return value
	}
	return nil
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
