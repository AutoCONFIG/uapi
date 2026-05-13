package relay

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BillingService struct {
	db *gorm.DB
}

func NewBillingService(database *gorm.DB) *BillingService {
	return &BillingService{db: database}
}

// CheckLimit checks if the token is within its plan limits. Returns error if exceeded.
func (b *BillingService) CheckLimit(tokenID string) error {
	var tp db.TokenPlan
	if err := b.db.Where("token_id = ?", tokenID).First(&tp).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // no plan = unlimited
		}
		return err
	}

	var plan db.Plan
	if err := b.db.First(&plan, "id = ? AND enabled = true", tp.PlanID).Error; err != nil {
		return err
	}

	switch plan.Type {
	case "count_based":
		return b.checkCountLimit(&tp, &plan)
	case "token_based":
		return b.checkTokenLimit(&tp, &plan)
	}
	return nil
}

func (b *BillingService) checkCountLimit(tp *db.TokenPlan, plan *db.Plan) error {
	limits := parseMap(plan.Limits)
	usage := parseMap(tp.WindowUsage)
	resets := parseMap(tp.WindowResetAt)
	now := time.Now()

	for window, maxCount := range limits {
		maxCountFloat, ok := toFloat(maxCount)
		if !ok {
			continue
		}
		maxCountInt := int(maxCountFloat)
		currentUsage := 0
		if u, ok := usage[window]; ok {
			if f, ok := toFloat(u); ok {
				currentUsage = int(f)
			}
		}

		if resetAtStr, hasReset := resets[window]; hasReset {
			if s, ok := resetAtStr.(string); ok {
				if resetAt, err := time.Parse(time.RFC3339, s); err == nil && now.After(resetAt) {
					currentUsage = 0
				}
			}
		}

		if currentUsage >= maxCountInt {
			return fmt.Errorf("rate limit exceeded for window %s", window)
		}
	}
	return nil
}

func (b *BillingService) checkTokenLimit(tp *db.TokenPlan, plan *db.Plan) error {
	if plan.TokenQuota > 0 && tp.UsedQuota >= plan.TokenQuota {
		return fmt.Errorf("token quota exceeded")
	}
	return nil
}

// PreConsume increments count usage or pre-deducts token quota.
func (b *BillingService) PreConsume(tokenID string, model string, estimatedTokens int) error {
	var tp db.TokenPlan
	if err := b.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("token_id = ?", tokenID).First(&tp).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	var plan db.Plan
	if err := b.db.First(&plan, "id = ?", tp.PlanID).Error; err != nil {
		return err
	}

	switch plan.Type {
	case "count_based":
		return b.incrementCountUsage(&tp, &plan)
	case "token_based":
		return b.preConsumeTokens(&tp, estimatedTokens)
	}
	return nil
}

func (b *BillingService) incrementCountUsage(tp *db.TokenPlan, plan *db.Plan) error {
	usage := parseMap(tp.WindowUsage)
	resets := parseMap(tp.WindowResetAt)
	limits := parseMap(plan.Limits)
	now := time.Now()

	for window := range limits {
		if resetAtStr, hasReset := resets[window]; hasReset {
			if s, ok := resetAtStr.(string); ok {
				if resetAt, err := time.Parse(time.RFC3339, s); err == nil && now.After(resetAt) {
					usage[window] = float64(0)
				}
			}
		}
		if _, ok := usage[window]; !ok {
			usage[window] = float64(0)
		}
		if f, ok := toFloat(usage[window]); ok {
			usage[window] = f + 1
		} else {
			usage[window] = float64(1)
		}

		if _, hasReset := resets[window]; !hasReset {
			duration := parseWindowDuration(window)
			resets[window] = now.Add(duration).Format(time.RFC3339)
		}
	}

	usageJSON, _ := json.Marshal(usage)
	resetsJSON, _ := json.Marshal(resets)
	return b.db.Model(tp).Updates(map[string]interface{}{
		"window_usage":    string(usageJSON),
		"window_reset_at": string(resetsJSON),
	}).Error
}

func (b *BillingService) preConsumeTokens(tp *db.TokenPlan, amount int) error {
	return b.db.Model(tp).Update("used_quota", gorm.Expr("used_quota + ?", amount)).Error
}

// Settle adjusts token usage after actual response.
func (b *BillingService) Settle(tokenID string, promptTokens, completionTokens int, model string) error {
	var tp db.TokenPlan
	if err := b.db.Where("token_id = ?", tokenID).First(&tp).Error; err != nil {
		return err
	}

	var plan db.Plan
	if err := b.db.First(&plan, "id = ?", tp.PlanID).Error; err != nil {
		return err
	}

	if plan.Type == "count_based" {
		// For count-based, no settlement needed — PreConsume already incremented
		return nil
	}

	// token_based: calculate actual cost with model ratio and adjust
	ratios := parseMap(plan.ModelRatios)
	ratio := 1.0
	if r, ok := ratios[model]; ok {
		if f, ok := toFloat(r); ok {
			ratio = f
		}
	}

	// actual cost in "tokens"
	actual := float64(promptTokens) + float64(completionTokens)*ratio
	// We pre-consumed promptTokens + completionTokens as rough estimate
	estimate := float64(promptTokens + completionTokens)
	delta := int(actual - estimate)

	if delta != 0 {
		return b.db.Model(&tp).Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
	}
	return nil
}

// Refund returns pre-consumed tokens on failure.
func (b *BillingService) Refund(tokenID string, amount int) error {
	var tp db.TokenPlan
	if err := b.db.Where("token_id = ?", tokenID).First(&tp).Error; err != nil {
		return err
	}
	return b.db.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", amount)).Error
}

// Helper functions

func parseMap(jsonStr string) map[string]interface{} {
	var result map[string]interface{}
	json.Unmarshal([]byte(jsonStr), &result)
	if result == nil {
		result = make(map[string]interface{})
	}
	return result
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func parseWindowDuration(window string) time.Duration {
	if len(window) < 2 {
		return time.Hour
	}
	var val int
	var unit string
	fmt.Sscanf(window, "%d%s", &val, &unit)
	switch unit {
	case "h":
		return time.Duration(val) * time.Hour
	case "d":
		return time.Duration(val) * 24 * time.Hour
	case "w":
		return time.Duration(val) * 7 * 24 * time.Hour
	default:
		return time.Hour
	}
}
