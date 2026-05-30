package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BillingService struct {
	db *gorm.DB
}

var ErrNoActiveSubscription = errors.New("no active subscription")

func NewBillingService(database *gorm.DB) *BillingService {
	return &BillingService{db: database}
}

// CheckLimit verifies the token has an active subscription and a supported plan.
// Actual quota consumption is enforced by PreConsume against the plan policy windows.
func (b *BillingService) CheckLimit(tokenID string) error {
	var tp db.TokenPlan
	if err := latestActiveTokenPlan(b.db, tokenID).First(&tp).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return ErrNoActiveSubscription
		}
		return err
	}

	var plan db.Plan
	if err := b.db.First(&plan, "id = ? AND enabled = true AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoActiveSubscription
		}
		return err
	}

	switch plan.Type {
	case "count_based", "token_based":
		return nil
	}
	return fmt.Errorf("unsupported plan type: %s", plan.Type)
}

// PreConsume applies the current usage window charge and returns the active subscription ID.
func (b *BillingService) PreConsume(tokenID string, model string, estimatedTokens int) (uuid.UUID, error) {
	var planID uuid.UUID
	err := b.db.Transaction(func(tx *gorm.DB) error {
		var tp db.TokenPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Scopes(scopeLatestActiveTokenPlan(tokenID)).
			First(&tp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoActiveSubscription
			}
			return err
		}
		planID = tp.ID
		var plan db.Plan
		if err := tx.First(&plan, "id = ? AND enabled = true AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoActiveSubscription
			}
			return err
		}
		switch plan.Type {
		case "count_based":
			return applyPolicyWindowDeltaTx(tx, plan.PolicyID, tp.UserID, tp.StartsAt, preConsumeChargeForPlan(plan.Type, 0, model, b.modelRatios()), true)
		case "token_based":
			return applyPolicyWindowDeltaTx(tx, plan.PolicyID, tp.UserID, tp.StartsAt, preConsumeChargeForPlan(plan.Type, estimatedTokens, model, b.modelRatios()), true)
		}
		return fmt.Errorf("unsupported plan type: %s", plan.Type)
	})
	return planID, err
}

func (b *BillingService) modelRatios() string {
	if b != nil && b.db != nil {
		return appsettings.Get(b.db, appsettings.ModelRatios, "{}")
	}
	return "{}"
}

func (b *BillingService) DBTransactionRefundAndSettle(tokenID string, tokenPlanID uuid.UUID, estTokens int, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens int, model string) error {
	return b.db.Transaction(func(tx *gorm.DB) error {
		return refundAndSettleTxForPlan(tx, tokenID, tokenPlanID, estTokens, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens, model, b.modelRatios())
	})
}

func RefundAndSettleTxForPlan(tx *gorm.DB, tokenID string, tokenPlanID uuid.UUID, estTokens int, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens int, model string) error {
	return refundAndSettleTxForPlan(tx, tokenID, tokenPlanID, estTokens, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens, model, "")
}

func RefundAndSettleTxForPlanWithRatios(tx *gorm.DB, tokenID string, tokenPlanID uuid.UUID, estTokens int, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens int, model, rawRatios string) error {
	return refundAndSettleTxForPlan(tx, tokenID, tokenPlanID, estTokens, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens, model, rawRatios)
}

func refundAndSettleTxForPlan(tx *gorm.DB, tokenID string, tokenPlanID uuid.UUID, estTokens int, promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens int, model, rawRatios string) error {
	// Cache tokens are billed at reduced rates:
	// cache_creation tokens: 1.25x prompt token cost (provider writes to cache)
	// cache_read tokens: 0.1x prompt token cost (cache hit, much cheaper)
	const cacheCreationRatio = 1.25
	const cacheReadRatio = 0.1

	if tokenPlanID == uuid.Nil {
		return ErrNoActiveSubscription
	}
	var tp db.TokenPlan
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"})
	q = q.Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.deleted_at IS NULL", tokenID).
		Where("token_plans.id = ?", tokenPlanID)
	if err := q.First(&tp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // no plan = nothing to settle
		}
		return err
	}

	var plan db.Plan
	planQuery := "id = ? AND enabled = true AND deleted_at IS NULL"
	if tokenPlanID != uuid.Nil {
		planQuery = "id = ? AND deleted_at IS NULL"
	}
	if err := tx.First(&plan, planQuery, tp.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	if plan.Type == "count_based" {
		// Count-based plans are fully charged at pre-consume time.
		return nil
	}

	if rawRatios == "" {
		rawRatios = "{}"
	}
	ratio, err := modelRatio(rawRatios, model)
	if err != nil {
		return fmt.Errorf("parse model ratios: %w", err)
	}

	// Cache tokens are included in prompt-equivalent tokens at reduced rates
	cacheEquivalent := int(float64(cacheCreationTokens)*cacheCreationRatio) +
		int(float64(cacheReadTokens)*cacheReadRatio)
	actual := int(math.Ceil(float64(promptTokens+cacheEquivalent+completionTokens) * ratio))
	if actual <= 0 {
		actual = preConsumeChargeForPlan(plan.Type, estTokens, model, rawRatios)
	}
	preConsumed := preConsumeChargeForPlan(plan.Type, estTokens, model, rawRatios)
	if err := applyPolicyWindowDeltaTx(tx, plan.PolicyID, tp.UserID, tp.StartsAt, actual-preConsumed, false); err != nil {
		return err
	}
	return nil
}

func (b *BillingService) DBTransactionRefund(tokenID string, tokenPlanID uuid.UUID, amount int) error {
	return b.db.Transaction(func(tx *gorm.DB) error {
		return RefundTxForPlan(tx, tokenID, tokenPlanID, amount)
	})
}

func (b *BillingService) DBTransactionRefundPreConsume(tokenID string, tokenPlanID uuid.UUID, estTokens int, model string) error {
	return b.db.Transaction(func(tx *gorm.DB) error {
		return RefundPreConsumeTxForPlanWithRatios(tx, tokenID, tokenPlanID, estTokens, model, b.modelRatios())
	})
}

func RefundTxForPlan(tx *gorm.DB, tokenID string, tokenPlanID uuid.UUID, amount int) error {
	if tokenPlanID == uuid.Nil {
		return ErrNoActiveSubscription
	}
	var tp db.TokenPlan
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"})
	q = q.Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.deleted_at IS NULL", tokenID).
		Where("token_plans.id = ?", tokenPlanID)
	if err := q.First(&tp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // no plan = nothing to refund
		}
		return err
	}

	var plan db.Plan
	planQuery := "id = ? AND enabled = true AND deleted_at IS NULL"
	if tokenPlanID != uuid.Nil {
		planQuery = "id = ? AND deleted_at IS NULL"
	}
	if err := tx.First(&plan, planQuery, tp.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	switch plan.Type {
	case "count_based":
		return nil
	default:
		return applyPolicyWindowDeltaTx(tx, plan.PolicyID, tp.UserID, tp.StartsAt, -amount, false)
	}
}

func RefundPreConsumeTxForPlanWithRatios(tx *gorm.DB, tokenID string, tokenPlanID uuid.UUID, estTokens int, model, rawRatios string) error {
	if tokenPlanID == uuid.Nil {
		return ErrNoActiveSubscription
	}
	var tp db.TokenPlan
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"})
	q = q.Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.deleted_at IS NULL", tokenID).
		Where("token_plans.id = ?", tokenPlanID)
	if err := q.First(&tp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	var plan db.Plan
	if err := tx.First(&plan, "id = ? AND deleted_at IS NULL", tp.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	amount := preConsumeChargeForPlan(plan.Type, estTokens, model, rawRatios)
	if amount <= 0 {
		return nil
	}
	return applyPolicyWindowDeltaTx(tx, plan.PolicyID, tp.UserID, tp.StartsAt, -amount, false)
}

func applyPolicyWindowDeltaTx(tx *gorm.DB, policyID *uuid.UUID, userID string, planStartsAt time.Time, delta int, enforce bool) error {
	if delta == 0 {
		return nil
	}
	if policyID == nil || *policyID == uuid.Nil {
		if enforce {
			return ErrNoActiveSubscription
		}
		return nil
	}
	var policy db.AccessPolicy
	if err := tx.Where("id = ? AND enabled = true AND deleted_at IS NULL", *policyID).First(&policy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoActiveSubscription
		}
		return err
	}
	now := time.Now().UTC()
	fiveHourStart, err := rollingFiveHourStartTx(tx, policy.ID, userID, now)
	if err != nil {
		return err
	}
	windows := []struct {
		name  string
		limit int
		start time.Time
	}{
		{name: "hour", limit: policy.HourlyLimit, start: fiveHourStart},
		{name: "week", limit: policy.WeeklyLimit, start: fixedWeekFromPlanStart(planStartsAt, now)},
		{name: "month", limit: policy.MonthlyLimit, start: fixedMonthFromPlanStart(planStartsAt, now)},
	}
	for _, window := range windows {
		var usage db.PolicyUsageWindow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("policy_id = ? AND user_id = ? AND window_type = ? AND window_start = ?", policy.ID, userID, window.name, window.start).
			First(&usage).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			usage = db.PolicyUsageWindow{
				ID:          uuid.New(),
				PolicyID:    policy.ID,
				UserID:      userID,
				WindowType:  window.name,
				WindowStart: window.start,
				UsedCount:   0,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&usage).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("policy_id = ? AND user_id = ? AND window_type = ? AND window_start = ?", policy.ID, userID, window.name, window.start).
				First(&usage).Error; err != nil {
				return err
			}
		}
		next := usage.UsedCount + delta
		if enforce && delta > 0 && next > window.limit {
			return fmt.Errorf("%s usage limit exceeded", window.name)
		}
		if next < 0 {
			next = 0
		}
		if err := tx.Model(&usage).Update("used_count", next).Error; err != nil {
			return err
		}
	}
	return nil
}

func rollingFiveHourStartTx(tx *gorm.DB, policyID uuid.UUID, userID string, now time.Time) (time.Time, error) {
	lockKey := "policy_window:" + policyID.String() + ":" + userID + ":hour"
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
		return time.Time{}, err
	}
	var usage db.PolicyUsageWindow
	err := tx.Where("policy_id = ? AND user_id = ? AND window_type = ? AND window_start <= ?", policyID, userID, "hour", now).
		Order("window_start DESC").
		First(&usage).Error
	if err == nil && now.Before(usage.WindowStart.UTC().Add(5*time.Hour)) {
		return usage.WindowStart.UTC(), nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, err
	}
	return now, nil
}

func fixedWeekFromPlanStart(planStartsAt time.Time, now time.Time) time.Time {
	start := planStartsAt.UTC()
	if start.IsZero() || now.Before(start) {
		return now
	}
	elapsed := now.Sub(start)
	return start.Add(time.Duration(int64(elapsed/(7*24*time.Hour))) * 7 * 24 * time.Hour)
}

func fixedMonthFromPlanStart(planStartsAt time.Time, now time.Time) time.Time {
	start := planStartsAt.UTC()
	if start.IsZero() || now.Before(start) {
		return now
	}
	elapsed := now.Sub(start)
	return start.Add(time.Duration(int64(elapsed/(30*24*time.Hour))) * 30 * 24 * time.Hour)
}

func latestActiveTokenPlan(tx *gorm.DB, tokenID string) *gorm.DB {
	return tx.Scopes(scopeLatestActiveTokenPlan(tokenID))
}

func scopeLatestActiveTokenPlan(tokenID string) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		return tx.
			Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.enabled = true AND tokens.deleted_at IS NULL", tokenID).
			Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
			Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", time.Now(), time.Now()).
			Order("token_plans.created_at DESC")
	}
}

// CheckUserPlan verifies the user is active and the token has an active plan.
func (b *BillingService) CheckUserPlan(userID string, tokenID string) error {
	var user db.User
	if err := b.db.Where("id = ? AND status = 'active' AND deleted_at IS NULL", userID).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("user not found or inactive")
		}
		return err
	}
	var tp db.TokenPlan
	if err := latestActiveTokenPlan(b.db.Model(&db.TokenPlan{}), tokenID).First(&tp).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("check plan: %w", err)
	}
	if tp.ID == uuid.Nil {
		return ErrNoActiveSubscription
	}
	return nil
}

// Helper functions

func parseMap(jsonStr string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if jsonStr == "" {
		return map[string]interface{}{}, nil
	}
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
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func applyModelRatio(tokens int, model, rawRatios string) int {
	if tokens <= 0 {
		return tokens
	}
	ratio, err := modelRatio(rawRatios, model)
	if err != nil {
		return tokens
	}
	charged := int(math.Ceil(float64(tokens) * ratio))
	if charged < 1 && ratio > 0 {
		return 1
	}
	return charged
}

func modelRatio(rawRatios, model string) (float64, error) {
	ratios, err := parseMap(rawRatios)
	if err != nil {
		return 1, err
	}
	ratio := 1.0
	if r, ok := ratios[model]; ok {
		if f, ok := toFloat(r); ok && f >= 0 {
			ratio = f
		}
	}
	return ratio, nil
}

func preConsumeChargeForPlan(planType string, estimatedTokens int, model, rawRatios string) int {
	switch planType {
	case "count_based":
		return applyModelRatio(1, model, rawRatios)
	case "token_based":
		if estimatedTokens <= 0 {
			estimatedTokens = 1
		}
		return applyModelRatio(estimatedTokens, model, rawRatios)
	default:
		return 0
	}
}
