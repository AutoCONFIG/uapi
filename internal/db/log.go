package db

import (
	"time"

	"github.com/google/uuid"
)

type Log struct {
	ID                  int64                  `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt           time.Time              `gorm:"index" json:"created_at"`
	TokenID             uuid.UUID              `gorm:"type:uuid;index" json:"token_id"`
	ClientIP            string                 `gorm:"size:50;index" json:"client_ip,omitempty"`
	ChannelID           uuid.UUID              `gorm:"type:uuid;index" json:"channel_id"`
	AccountID           uuid.UUID              `gorm:"type:uuid;index" json:"account_id"`
	Model               string                 `gorm:"size:100;index" json:"model"`
	RoutedModel         string                 `gorm:"size:100;index" json:"routed_model"`
	ClientFormat        string                 `gorm:"size:50;index" json:"client_format"`
	UpstreamFormat      string                 `gorm:"size:50;index" json:"upstream_format"`
	IsStream            bool                   `json:"is_stream"`
	PromptTokens        int64                  `json:"prompt_tokens"`
	CompletionTokens    int64                  `json:"completion_tokens"`
	CacheCreationTokens int64                  `gorm:"default:0" json:"cache_creation_tokens"`
	CacheReadTokens     int64                  `gorm:"default:0" json:"cache_read_tokens"`
	TotalTokens         int64                  `json:"total_tokens"`
	LatencyMs           int64                  `json:"latency_ms"`
	StatusCode          int                    `json:"status_code"`
	ErrorMessage        string                 `gorm:"type:text" json:"error_message,omitempty"`
	AdminInfo           map[string]interface{} `gorm:"serializer:json;type:jsonb" json:"admin_info,omitempty"`
}

func (Log) TableName() string { return "logs" }
