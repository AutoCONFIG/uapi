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

	// Navigate to rate_limits
	limits := raw
	if rl, ok := raw["rate_limits"].(map[string]interface{}); ok {
		limits = rl
	} else if rl, ok := raw["rateLimits"].(map[string]interface{}); ok {
		limits = rl
	}

	addCodexWindow(qd, "Codex 主窗口", mapValue(limits, "primary"))
	addCodexWindow(qd, "Codex 周窗口", mapValue(limits, "secondary"))

	// Credits
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

	// Tier from plan type
	if planType, ok := raw["plan_type"].(string); ok && planType != "" {
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

func codexRemainingPercent(m map[string]interface{}) *int {
	for _, key := range []string{"remaining_percent", "remainingPercent"} {
		if v, ok := m[key].(float64); ok {
			pct := int(v)
			return &pct
		}
	}
	for _, key := range []string{"remaining_fraction", "remainingFraction"} {
		if v, ok := m[key].(float64); ok {
			pct := int(v * 100)
			return &pct
		}
	}
	for _, key := range []string{"used_percent", "usedPercent", "utilization"} {
		if v, ok := m[key].(float64); ok {
			pct := 100 - int(v)
			return &pct
		}
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
