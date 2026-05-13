package db

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type Base struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

type Channel struct {
	Base
	Name     string `gorm:"size:100;not null" json:"name"`
	Type     string `gorm:"size:50;not null" json:"type"` // openai, anthropic, gemini
	Endpoint string `gorm:"size:500;not null" json:"endpoint"`
	Enabled  bool   `gorm:"default:true" json:"enabled"`
	Models   string `gorm:"type:text" json:"models"` // comma-separated
	Priority int    `gorm:"default:0" json:"priority"`
}

func (Channel) TableName() string { return "channels" }

type Account struct {
	Base
	ChannelID     uuid.UUID  `gorm:"type:uuid;index;not null" json:"channel_id"`
	Name          string     `gorm:"size:100;not null" json:"name"`
	Credentials   string     `gorm:"type:text;not null" json:"-"` // AES-256-GCM encrypted
	Weight        int        `gorm:"default:1" json:"weight"`
	Enabled       bool       `gorm:"default:true" json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
}

func (Account) TableName() string { return "accounts" }

type Token struct {
	Base
	Name         string `gorm:"size:100;not null" json:"name"`
	Key          string `gorm:"size:100;uniqueIndex;not null" json:"key"`
	Enabled      bool   `gorm:"default:true" json:"enabled"`
	IPWhitelist  string `gorm:"type:text" json:"ip_whitelist"`
	Unlimited    bool   `gorm:"default:false" json:"unlimited"`
}

func (Token) TableName() string { return "tokens" }

type Plan struct {
	Base
	Name            string `gorm:"size:100;not null" json:"name"`
	Type            string `gorm:"size:20;not null" json:"type"` // count_based, token_based
	Limits          string `gorm:"type:jsonb" json:"limits"`
	ModelRatios     string `gorm:"type:jsonb" json:"model_ratios"`
	CompletionRatio string `gorm:"type:jsonb" json:"completion_ratio"`
	TokenQuota      int64  `json:"token_quota"`
	Enabled         bool   `gorm:"default:true" json:"enabled"`
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

type Log struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt        time.Time `gorm:"index" json:"created_at"`
	TokenID          uuid.UUID `gorm:"type:uuid;index" json:"token_id"`
	ChannelID        uuid.UUID `gorm:"type:uuid;index" json:"channel_id"`
	AccountID        uuid.UUID `gorm:"type:uuid;index" json:"account_id"`
	Model            string    `gorm:"size:100;index" json:"model"`
	IsStream         bool      `json:"is_stream"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	LatencyMs        int       `json:"latency_ms"`
	StatusCode       int       `json:"status_code"`
	ErrorMessage     string    `gorm:"type:text" json:"error_message,omitempty"`
}

func (Log) TableName() string { return "logs" }

type AuditLog struct {
	ID         int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
	User       string    `gorm:"size:100;not null" json:"user"`
	Action     string    `gorm:"size:50;not null" json:"action"`
	Resource   string    `gorm:"size:50;not null" json:"resource"`
	ResourceID string   `gorm:"size:100" json:"resource_id"`
	OldValue   string    `gorm:"type:text" json:"old_value,omitempty"`
	NewValue   string    `gorm:"type:text" json:"new_value,omitempty"`
	IPAddress  string    `gorm:"size:50" json:"ip_address"`
}

func (AuditLog) TableName() string { return "audit_log" }

var AllModels = []interface{}{
	&Channel{}, &Account{}, &Token{}, &Plan{}, &TokenPlan{}, &Log{}, &AuditLog{},
}

func Init(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	db.Exec("CREATE EXTENSION IF NOT EXISTS \"pgcrypto\"")
	if err := db.AutoMigrate(AllModels...); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	return db, nil
}
