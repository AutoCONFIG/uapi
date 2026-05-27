package quota

import (
	"fmt"
	"strings"

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
	if rl, ok := raw["rate_limits"].(map[string]interface{}); ok {
		limits = rl
	} else if rl, ok := raw["rateLimits"].(map[string]interface{}); ok {
		limits = rl
	}

	addCodexWindow(qd, "Codex 主窗口", mapValue(limits, "primary"))
	addCodexWindow(qd, "Codex 周窗口", mapValue(limits, "secondary"))
	collectCodexWindows(qd, "", limits)

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
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            label,
			RemainingPercent: clampPercent(*remainingPct),
			ResetTime:        codexResetTime(window),
		})
	}
}

func collectCodexWindows(qd *QuotaData, prefix string, m map[string]interface{}) {
	if m == nil {
		return
	}
	if remainingPct := codexRemainingPercent(m); remainingPct != nil {
		label := firstString(m, "label", "name", "model", "model_id", "modelId", "type", "window")
		if label == "" {
			label = prefix
		}
		if label == "" {
			label = "Codex 额度"
		}
		key := "Codex " + label
		if !quotaBucketExists(qd, key) {
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            key,
				RemainingPercent: clampPercent(*remainingPct),
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
		if v, ok := m[key].(float64); ok {
			return fmt.Sprintf("%d", int64(v))
		}
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
