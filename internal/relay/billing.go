package relay

import (
	"encoding/json"
	"errors"
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
	if err := b.db.First(&plan, "id = ? AND enabled = true AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // plan removed = no rate limit
		}
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
	return b.db.Transaction(func(tx *gorm.DB) error {
		var tp db.TokenPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_id = ?", tokenID).
			Order("created_at DESC").First(&tp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // no plan = unlimited
			}
			return err
		}
		var plan db.Plan
		if err := tx.First(&plan, "id = ? AND enabled = true AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
			return nil
		}
		switch plan.Type {
		case "count_based":
			return b.incrementCountUsageTx(tx, &tp, &plan)
		case "token_based":
			return b.preConsumeTokensTx(tx, &tp, estimatedTokens)
		}
		return nil
	})
}

func (b *BillingService) incrementCountUsageTx(tx *gorm.DB, tp *db.TokenPlan, plan *db.Plan) error {
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
	return tx.Model(tp).Updates(map[string]interface{}{
		"window_usage":    string(usageJSON),
		"window_reset_at": string(resetsJSON),
	}).Error
}

func (b *BillingService) preConsumeTokensTx(tx *gorm.DB, tp *db.TokenPlan, amount int) error {
	return tx.Model(tp).Update("used_quota", gorm.Expr("used_quota + ?", amount)).Error
}

// RefundAndSettle atomically refunds the pre-consumed estimate and settles actual usage.
// This prevents the race where Refund runs after Settle, undoing legitimate billing.
func (b *BillingService) RefundAndSettle(tokenID string, estTokens int, promptTokens, completionTokens int, model string) error {
	return b.db.Transaction(func(tx *gorm.DB) error {
		var tp db.TokenPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_id = ?", tokenID).
			First(&tp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // no plan = nothing to settle
			}
			return err
		}

		var plan db.Plan
		if err := tx.First(&plan, "id = ? AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
			return err
		}

		// 1. Refund the pre-consumed estimate (only for token_based plans).
		// For count_based plans, the PreConsume increment IS the final usage — it must NOT be refunded.
		if plan.Type != "count_based" {
			if err := tx.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", estTokens)).Error; err != nil {
				return err
			}
		}

		// 2. Settle actual usage
		if plan.Type == "count_based" {
			return nil
		}

		// token_based: record actual cost
		ratios := parseMap(plan.ModelRatios)
		ratio := 1.0
		if r, ok := ratios[model]; ok {
			if f, ok := toFloat(r); ok {
				ratio = f
			}
		}

		actual := int(float64(promptTokens) + float64(completionTokens)*ratio)
		if actual > 0 {
			return tx.Model(&tp).Update("used_quota", gorm.Expr("used_quota + ?", actual)).Error
		}
		return nil
	})
}

// Refund returns pre-consumed tokens on failure or after actual usage is settled.
func (b *BillingService) Refund(tokenID string, amount int) error {
	return b.db.Transaction(func(tx *gorm.DB) error {
		var tp db.TokenPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_id = ?", tokenID).
			First(&tp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // no plan = nothing to refund
			}
			return err
		}

		var plan db.Plan
		if err := tx.First(&plan, "id = ? AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
			// Can't determine plan type, try token refund anyway
			return tx.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", amount)).Error
		}

		switch plan.Type {
		case "count_based":
			return b.decrementCountUsageTx(tx, &tp, &plan, 1)
		default:
			return tx.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", amount)).Error
		}
	})
}

func (b *BillingService) decrementCountUsageTx(tx *gorm.DB, tp *db.TokenPlan, plan *db.Plan, amount int) error {
	usage := parseMap(tp.WindowUsage)
	limits := parseMap(plan.Limits)

	for window := range limits {
		if _, ok := usage[window]; !ok {
			continue
		}
		if f, ok := toFloat(usage[window]); ok {
			usage[window] = f - float64(amount)
			if f-float64(amount) < 0 {
				usage[window] = float64(0)
			}
		}
	}

	usageJSON, _ := json.Marshal(usage)
	return tx.Model(tp).Update("window_usage", string(usageJSON)).Error
}

// CheckUserBalance verifies the user has a positive balance. Returns error if insufficient.
// Users with active plans skip the balance check since plans have their own quota enforcement.
func (b *BillingService) CheckUserBalance(userID string) error {
	var user db.User
	if err := b.db.Where("id = ? AND status = 'active' AND deleted_at IS NULL", userID).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("user not found or inactive")
		}
		return err
	}

	// If the user has tokens with active plans, skip balance check — plan quotas are enforced separately
	var planCount int64
	b.db.Model(&db.TokenPlan{}).
		Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
		Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
		Count(&planCount)
	if planCount > 0 {
		return nil
	}

	if user.Balance <= 0 {
		return fmt.Errorf("insufficient user balance")
	}
	return nil
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
