package quota

import (
	"fmt"

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

	// Primary window (short, e.g. 5h)
	if primary, ok := limits["primary"].(map[string]interface{}); ok {
		usedPct := codexUsedPercent(primary)
		if usedPct != nil {
			remaining := 100 - *usedPct
			if remaining < 0 {
				remaining = 0
			}
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            "Codex 主窗口",
				RemainingPercent: remaining,
				ResetTime:        codexResetTime(primary),
			})
		}
	}

	// Secondary window (weekly)
	if secondary, ok := limits["secondary"].(map[string]interface{}); ok {
		usedPct := codexUsedPercent(secondary)
		if usedPct != nil {
			remaining := 100 - *usedPct
			if remaining < 0 {
				remaining = 0
			}
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            "Codex 周窗口",
				RemainingPercent: remaining,
				ResetTime:        codexResetTime(secondary),
			})
		}
	}

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

func codexUsedPercent(m map[string]interface{}) *int {
	for _, key := range []string{"used_percent", "usedPercent"} {
		if v, ok := m[key].(float64); ok {
			pct := int(v)
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
