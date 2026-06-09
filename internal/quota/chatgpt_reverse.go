package quota

import (
	"fmt"
)

func init() {
	Register("chatgpt_reverse", &chatgptReverseFetcher{})
}

type chatgptReverseFetcher struct{}

func (c *chatgptReverseFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	// ChatGPT reverse channel uses web API, which has limited quota information
	// We can infer quota from response headers or metadata

	qd := &QuotaData{}

	// Check if we have quota information in metadata
	if quota, ok := metadata["image_quota"].(float64); ok && quota >= 0 {
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            "作图额度",
			RemainingPercent: int(quota),
			ResetTime:        "",
		})
	}

	// Check for image quota unknown flag
	if unknown, ok := metadata["image_quota_unknown"].(bool); ok && unknown {
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            "作图额度 (未知)",
			RemainingPercent: 100,
			ResetTime:        "",
		})
	}

	// If no quota information, add a default bucket
	if len(qd.Buckets) == 0 {
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            "作图额度",
			RemainingPercent: 100,
			ResetTime:        "",
		})
	}

	return qd, nil
}

// chatgptReverseQuotaLabel generates a friendly label for ChatGPT reverse quota buckets
func chatgptReverseQuotaLabel(bucketType string, index int) string {
	switch bucketType {
	case "image":
		return "作图额度"
	case "text":
		return "文本额度"
	default:
		return fmt.Sprintf("ChatGPT 额度桶 %d", index+1)
	}
}
