package db

import (
	"time"

	"github.com/google/uuid"
)

type Plan struct {
	Base
	Name         string     `gorm:"size:100;not null" json:"name"`
	Type         string     `gorm:"size:20;not null" json:"type"`
	PolicyID     *uuid.UUID `gorm:"type:uuid;index" json:"policy_id,omitempty"`
	Enabled      bool       `gorm:"default:true" json:"enabled"`
	Public       bool       `gorm:"column:is_public;default:false;index" json:"public"`
	DurationDays int        `gorm:"default:30" json:"duration_days"`
	Price        float64    `gorm:"type:numeric(12,2);default:0" json:"price"`
}

func (Plan) TableName() string { return "plans" }

type TokenPlan struct {
	Base
	UserID    string    `gorm:"size:36;index;not null" json:"user_id"`
	PlanID    uuid.UUID `gorm:"type:uuid;index;not null" json:"plan_id"`
	StartsAt  time.Time `gorm:"index" json:"starts_at"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
}

func (TokenPlan) TableName() string { return "token_plans" }
