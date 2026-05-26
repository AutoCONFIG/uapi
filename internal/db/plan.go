package db

import (
	"time"

	"github.com/google/uuid"
)

type Plan struct {
	Base
	Name            string     `gorm:"size:100;not null" json:"name"`
	Type            string     `gorm:"size:20;not null" json:"type"`
	PolicyID        *uuid.UUID `gorm:"type:uuid;index" json:"policy_id,omitempty"`
	ModelRatios     string     `gorm:"type:jsonb" json:"model_ratios"`
	CompletionRatio string     `gorm:"type:jsonb" json:"completion_ratio"`
	CountQuota      int64      `json:"count_quota"`
	TokenQuota      int64      `json:"token_quota"`
	Enabled         bool       `gorm:"default:true" json:"enabled"`
	DurationDays    int        `gorm:"default:30" json:"duration_days"`
}

func (Plan) TableName() string { return "plans" }

type TokenPlan struct {
	Base
	UserID     string    `gorm:"size:36;index;not null" json:"user_id"`
	PlanID     uuid.UUID `gorm:"type:uuid;index;not null" json:"plan_id"`
	UsedCount  int64     `gorm:"default:0" json:"used_count"`
	UsedTokens int64     `gorm:"default:0" json:"used_tokens"`
	StartsAt   time.Time `gorm:"index" json:"starts_at"`
	ExpiresAt  time.Time `gorm:"index" json:"expires_at"`
}

func (TokenPlan) TableName() string { return "token_plans" }
