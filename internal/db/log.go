package db

import (
	"time"

	"github.com/google/uuid"
)

type Log struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt        time.Time `gorm:"index" json:"created_at"`
	TokenID          uuid.UUID `gorm:"type:uuid;index" json:"token_id"`
	ChannelID        uuid.UUID `gorm:"type:uuid;index" json:"channel_id"`
	AccountID        uuid.UUID `gorm:"type:uuid;index" json:"account_id"`
	Model            string    `gorm:"size:100;index" json:"model"`
	IsStream         bool      `json:"is_stream"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	LatencyMs        int64     `json:"latency_ms"`
	StatusCode       int       `json:"status_code"`
	ErrorMessage     string    `gorm:"type:text" json:"error_message,omitempty"`
}

func (Log) TableName() string { return "logs" }
