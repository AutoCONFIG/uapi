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

var AllModels = []interface{}{
	&Channel{}, &Account{}, &Token{}, &Plan{}, &TokenPlan{}, &Log{}, &AuditLog{},
	&User{}, &RedeemCode{}, &RelayNode{}, &NodeChannel{}, &AccessPolicy{}, &PolicyUsageWindow{}, &UsageEvent{},
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
	if err := migrateNodeChannelIndexes(db); err != nil {
		return nil, err
	}
	return db, nil
}

func migrateNodeChannelIndexes(db *gorm.DB) error {
	if err := db.Exec(`DROP INDEX IF EXISTS idx_node_channel`).Error; err != nil {
		return fmt.Errorf("drop legacy node channel index: %w", err)
	}
	if err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_node_channel_active
		ON node_channels (relay_node_id, channel_id)
		WHERE deleted_at IS NULL
	`).Error; err != nil {
		return fmt.Errorf("create active node channel index: %w", err)
	}
	return nil
}
