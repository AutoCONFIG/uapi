package db

import (
	"time"
)

type Token struct {
	Base
	UserID      string     `gorm:"size:36;index" json:"user_id"` // associated User
	Name        string     `gorm:"size:100;not null" json:"name"`
	Key         string     `gorm:"size:100;uniqueIndex;not null" json:"key"`
	Enabled     bool       `gorm:"default:true" json:"enabled"`
	IPWhitelist string     `gorm:"type:text" json:"ip_whitelist"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Models      string     `gorm:"type:text" json:"models"`
	Permissions string     `gorm:"type:text" json:"permissions"`
}

func (Token) TableName() string { return "tokens" }
