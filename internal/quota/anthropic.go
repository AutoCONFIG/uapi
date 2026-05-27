package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func init() {
	Register("claude_code", &anthropicFetcher{})
}

const anthropicUsageURL = "https://api.claude.ai/api/oauth/usage"

type anthropicFetcher struct{}

func (a *anthropicFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	usage, err := fetchAnthropicUsage(accessToken)
	if err != nil {
		return nil, err
	}
	return convertAnthropicUsage(usage, metadata), nil
}

func fetchAnthropicUsage(accessToken string) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, anthropicUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic usage request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read anthropic usage response: %w", err)
	}
	if resp.StatusCode == 403 {
		return map[string]interface{}{"_forbidden": true, "_forbidden_reason": "account_forbidden"}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic usage failed: status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	var usage map[string]interface{}
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("parse anthropic usage response: %w", err)
	}
	return usage, nil
}

var anthropicWindowLabels = map[string]string{
	"five_hour":            "Claude 5h 窗口",
	"seven_day":            "Claude 周窗口",
	"seven_day_sonnet":     "Claude Sonnet 周窗口",
	"seven_day_opus":       "Claude Opus 周窗口",
	"seven_day_oauth_apps": "Claude OAuth Apps 周窗口",
}

func convertAnthropicUsage(usage map[string]interface{}, metadata map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	// Check for forbidden flag
	if forbidden, ok := usage["_forbidden"].(bool); ok && forbidden {
		qd.IsForbidden = true
		qd.ForbiddenReason, _ = usage["_forbidden_reason"].(string)
		return qd
	}

	for key, label := range anthropicWindowLabels {
		window, ok := usage[key].(map[string]interface{})
		if !ok {
			continue
		}
		utilization, ok := window["utilization"].(float64)
		if !ok {
			continue
		}
		usedPercent := int(utilization)
		if usedPercent < 0 {
			usedPercent = 0
		}
		if usedPercent > 100 {
			usedPercent = 100
		}
		remaining := 100 - usedPercent

		var resetTime string
		if rt, ok := window["resets_at"].(string); ok {
			resetTime = rt
		}

		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            label,
			RemainingPercent: remaining,
			ResetTime:        resetTime,
		})
	}

	// Extract tier from metadata
	if sub, ok := metadata["subscription_type"].(string); ok && sub != "" {
		qd.Tier = sub
	}

	// Extract extra_usage credits
	if eu, ok := usage["extra_usage"].(map[string]interface{}); ok {
		enabled, _ := eu["is_enabled"].(bool)
		if enabled {
			var balance string
			if used, ok := eu["used_credits"].(float64); ok {
				if limit, ok := eu["monthly_limit"].(float64); ok && limit > 0 {
					remaining := int(limit) - int(used)
					if remaining < 0 {
						remaining = 0
					}
					balance = fmt.Sprintf("%d / %d", remaining, int(limit))
				}
			}
			qd.Credits = &CreditsInfo{
				Balance:   balance,
				Label:     "Extra Usage",
				Unlimited: false,
			}
		}
	}

	return qd
}

// truncate limits a byte slice to n bytes for safe inclusion in error messages.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
