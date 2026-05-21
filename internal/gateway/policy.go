package gateway

import (
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type policyRuntime struct {
	policy db.AccessPolicy
	ok     bool
}

func (g *Gateway) loadPolicy(token db.Token) (db.AccessPolicy, bool, error) {
	if token.PolicyID == nil || *token.PolicyID == uuid.Nil {
		return db.AccessPolicy{}, false, nil
	}
	var policy db.AccessPolicy
	if err := g.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *token.PolicyID).First(&policy).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return db.AccessPolicy{}, true, fmt.Errorf("access policy disabled or not found")
		}
		return db.AccessPolicy{}, true, err
	}
	return policy, true, nil
}

func (g *Gateway) checkPolicyWindows(policy db.AccessPolicy, tokenID uuid.UUID) error {
	windows := []struct {
		typeName string
		limit    int
		start    time.Time
	}{
		{"hour", policy.HourlyLimit, currentHour()},
		{"week", policy.WeeklyLimit, currentWeek()},
		{"month", policy.MonthlyLimit, currentMonth()},
	}
	return g.db.Transaction(func(tx *gorm.DB) error {
		for _, w := range windows {
			if w.limit <= 0 {
				continue
			}
			var usage db.PolicyUsageWindow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("policy_id = ? AND token_id = ? AND window_type = ? AND window_start = ?", policy.ID, tokenID, w.typeName, w.start).
				First(&usage).Error
			if err != nil {
				if err != gorm.ErrRecordNotFound {
					return err
				}
				newUsage := db.PolicyUsageWindow{
					ID:          uuid.New(),
					PolicyID:    policy.ID,
					TokenID:     tokenID,
					WindowType:  w.typeName,
					WindowStart: w.start,
					UsedCount:   0,
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newUsage).Error; err != nil {
					return err
				}
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("policy_id = ? AND token_id = ? AND window_type = ? AND window_start = ?", policy.ID, tokenID, w.typeName, w.start).
					First(&usage).Error; err != nil {
					return err
				}
			}
			if usage.UsedCount >= w.limit {
				return fmt.Errorf("%s request limit exceeded", w.typeName)
			}
			if err := tx.Model(&usage).Update("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func currentHour() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)
}

func currentWeek() time.Time {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
	return start
}

func currentMonth() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}
