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
	Status    string     `gorm:"size:20;default:active" json:"status"` // active, used, expired
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
}

func (RedeemCode) TableName() string { return "redeem_codes" }
