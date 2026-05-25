package db

import (
	"time"

	"github.com/google/uuid"
)

type RedeemCode struct {
	Base
	Code      string     `gorm:"size:100;uniqueIndex;not null" json:"code"`
	PlanID    uuid.UUID  `gorm:"type:uuid;index" json:"plan_id"`
	Value     int64      `json:"-"`
	UsedBy    *string    `gorm:"size:36;index" json:"used_by,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	MaxUses   int        `gorm:"default:1" json:"max_uses"`
	UsedCount int        `gorm:"default:0" json:"used_count"`
	Status    string     `gorm:"size:20;default:active" json:"status"` // active, used
}

func (RedeemCode) TableName() string { return "redeem_codes" }
