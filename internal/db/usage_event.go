package db

import (
	"time"

	"github.com/google/uuid"
)

type UsageEvent struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	RequestID        string    `gorm:"size:100;uniqueIndex;not null" json:"request_id"`
	TokenID          uuid.UUID `gorm:"type:uuid;index;not null" json:"token_id"`
	TokenPlanID      uuid.UUID `gorm:"type:uuid;index" json:"token_plan_id"`
	ChannelID        uuid.UUID `gorm:"type:uuid;index;not null" json:"channel_id"`
	AccountID        uuid.UUID `gorm:"type:uuid;index;not null" json:"account_id"`
	Model            string    `gorm:"size:100;index" json:"model"`
	IsStream         bool      `json:"is_stream"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	EstimatedTokens  int       `json:"estimated_tokens"`
	StatusCode       int       `json:"status_code"`
	LatencyMs        int64     `json:"latency_ms"`
	Settled          bool      `gorm:"default:false;index" json:"settled"`
	CreatedAt        time.Time `json:"created_at"`
}

func (UsageEvent) TableName() string { return "usage_events" }
