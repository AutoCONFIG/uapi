package db

import (
	"time"

	"github.com/google/uuid"
)

type Account struct {
	Base
	ChannelID     uuid.UUID              `gorm:"type:uuid;index;not null" json:"channel_id"`
	Name          string                 `gorm:"size:100;not null" json:"name"`
	Credentials   string                 `gorm:"type:text;not null" json:"-"`              // AES-256-GCM encrypted
	CredType      string                 `gorm:"size:20;default:api_key" json:"cred_type"` // api_key | oauth_token
	Weight        int                    `gorm:"default:1" json:"weight"`
	Enabled       bool                   `gorm:"default:true" json:"enabled"`
	CooldownUntil *time.Time             `json:"cooldown_until,omitempty"`
	RefreshToken  string                 `gorm:"type:text" json:"-"`     // AES encrypted (for oauth_token)
	TokenExpiry   *time.Time             `json:"token_expiry,omitempty"` // access_token expiry
	ClientID      string                 `gorm:"type:text" json:"-"`     // OAuth client ID
	ClientSecret  string                 `gorm:"type:text" json:"-"`     // AES encrypted OAuth client secret
	TokenURL      string                 `gorm:"type:text" json:"-"`     // OAuth token endpoint
	Metadata      map[string]interface{} `gorm:"serializer:json;type:jsonb" json:"metadata,omitempty"`
}

func (Account) TableName() string { return "accounts" }
