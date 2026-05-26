package db

import (
	"time"

	"github.com/google/uuid"
)

type AccessPolicy struct {
	Base
	AllowedModels  string `gorm:"type:text" json:"allowed_models"`
	MaxConcurrency int    `gorm:"default:0" json:"max_concurrency"`
	HourlyLimit    int    `gorm:"default:0" json:"hourly_limit"`
	WeeklyLimit    int    `gorm:"default:0" json:"weekly_limit"`
	MonthlyLimit   int    `gorm:"default:0" json:"monthly_limit"`
	Enabled        bool   `gorm:"default:true;index" json:"enabled"`
}

func (AccessPolicy) TableName() string { return "access_policies" }

type PolicyUsageWindow struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PolicyID    uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_policy_window_user" json:"policy_id"`
	UserID      string    `gorm:"size:36;index;not null;uniqueIndex:idx_policy_window_user" json:"user_id"`
	WindowType  string    `gorm:"size:20;not null;uniqueIndex:idx_policy_window_user" json:"window_type"`
	WindowStart time.Time `gorm:"not null;uniqueIndex:idx_policy_window_user" json:"window_start"`
	UsedCount   int       `gorm:"default:0" json:"used_count"`
}

func (PolicyUsageWindow) TableName() string { return "policy_usage_windows" }
