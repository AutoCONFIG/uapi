package db

import (
	"github.com/google/uuid"
)

type Plan struct {
	Base
	Name            string     `gorm:"size:100;not null" json:"name"`
	Type            string     `gorm:"size:20;not null" json:"type"`
	PolicyID        *uuid.UUID `gorm:"type:uuid;index" json:"policy_id,omitempty"`
	Limits          string     `gorm:"type:jsonb" json:"limits"`
	ModelRatios     string     `gorm:"type:jsonb" json:"model_ratios"`
	CompletionRatio string     `gorm:"type:jsonb" json:"completion_ratio"`
	TokenQuota      int64      `json:"token_quota"`
	Enabled         bool       `gorm:"default:true" json:"enabled"`
}

func (Plan) TableName() string { return "plans" }

type TokenPlan struct {
	Base
	TokenID       uuid.UUID `gorm:"type:uuid;index;not null" json:"token_id"`
	PlanID        uuid.UUID `gorm:"type:uuid;index;not null" json:"plan_id"`
	WindowUsage   string    `gorm:"type:jsonb" json:"window_usage"`
	WindowResetAt string    `gorm:"type:jsonb" json:"window_reset_at"`
	UsedQuota     int64     `gorm:"default:0" json:"used_quota"`
}

func (TokenPlan) TableName() string { return "token_plans" }
