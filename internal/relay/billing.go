package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
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
	limits, err := parseMap(plan.Limits)
	if err != nil {
		return fmt.Errorf("parse limits: %w", err)
	}
	usage, err := parseMap(tp.WindowUsage)
	if err != nil {
		return fmt.Errorf("parse usage: %w", err)
	}
	resets, err := parseMap(tp.WindowResetAt)
	if err != nil {
		return fmt.Errorf("parse resets: %w", err)
	}
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // no plan = unlimited
			}
			return err
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
	usage, err := parseMap(tp.WindowUsage)
	if err != nil {
		return fmt.Errorf("parse usage: %w", err)
	}
	resets, err := parseMap(tp.WindowResetAt)
	if err != nil {
		return fmt.Errorf("parse resets: %w", err)
	}
	limits, err := parseMap(plan.Limits)
	if err != nil {
		return fmt.Errorf("parse limits: %w", err)
	}
	now := time.Now()

	for window, maxCount := range limits {
		maxCountFloat, ok := toFloat(maxCount)
		if !ok {
			continue
		}
		maxCountInt := int(maxCountFloat)

		// Reset window if expired
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

		// Re-check limit inside transaction with row lock held
		if f, ok := toFloat(usage[window]); ok && int(f) >= maxCountInt {
			return fmt.Errorf("rate limit exceeded for window %s", window)
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

	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}
	resetsJSON, err := json.Marshal(resets)
	if err != nil {
		return fmt.Errorf("marshal resets: %w", err)
	}
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
		return RefundAndSettleTx(tx, tokenID, estTokens, promptTokens, completionTokens, model)
	})
}

func RefundAndSettleTx(tx *gorm.DB, tokenID string, estTokens int, promptTokens, completionTokens int, model string) error {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Plan deleted mid-request — best-effort refund of estimate
			tx.Model(&tp).Where("token_id = ?", tokenID).
				Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", estTokens))
			return nil
		}
		return err
	}

	// Refund the pre-consumed estimate only for token-based plans.
	if plan.Type != "count_based" {
		if err := tx.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", estTokens)).Error; err != nil {
			return err
		}
	}

	if plan.Type == "count_based" {
		return nil
	}

	ratios, err := parseMap(plan.ModelRatios)
	if err != nil {
		return fmt.Errorf("parse model ratios: %w", err)
	}
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
			// count_based plans: PreConsume is the final usage - no refund needed.
			return nil
		default:
			return tx.Model(&tp).Update("used_quota", gorm.Expr("GREATEST(0, used_quota - ?)", amount)).Error
		}
	})
}

// CheckUserBalance verifies the user has a positive balance. Returns error if insufficient.
// Users with an active plan on the specific token being used skip the balance check since plans have their own quota enforcement.
func (b *BillingService) CheckUserBalance(userID string, tokenID string) error {
	var user db.User
	if err := b.db.Where("id = ? AND status = 'active' AND deleted_at IS NULL", userID).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("user not found or inactive")
		}
		return err
	}

	// If the specific token has an active plan, skip balance check — plan quotas are enforced separately
	var planCount int64
	if err := b.db.Model(&db.TokenPlan{}).
		Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
		Where("token_plans.token_id = ?", tokenID).
		Count(&planCount).Error; err != nil {
		return fmt.Errorf("check plan: %w", err)
	}
	if planCount > 0 {
		return nil
	}

	if user.Balance <= 0 {
		return fmt.Errorf("insufficient user balance")
	}
	return nil
}

// Helper functions

func parseMap(jsonStr string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = make(map[string]interface{})
	}
	return result, nil
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
	case "m":
		return time.Duration(val) * time.Minute
	default:
		if unit == "" || val <= 0 {
			logger.Warnf("relay.billing", "invalid window duration defaulting to 1h", logger.F("window", window))
		}
		return time.Hour
	}
}
