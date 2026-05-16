package admin

import (
	"log"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"gorm.io/gorm"
)

// CleanupOldLogs deletes logs older than retentionDays.
func CleanupOldLogs(database *gorm.DB, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	return database.Where("created_at < ?", cutoff).Delete(&db.Log{}).Error
}

// StartLogCleanup runs periodic log cleanup.
func StartLogCleanup(database *gorm.DB, retentionDays int) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := CleanupOldLogs(database, retentionDays); err != nil {
				log.Printf("log cleanup failed: %v", err)
			}
		}
	}()
}

// InitPools loads all channels and their accounts into the pool manager at startup.
func InitPools(database *gorm.DB, setPool func(channelID string, accounts []*db.Account)) error {
	var channels []db.Channel
	if err := database.Where("enabled = true AND deleted_at IS NULL").Find(&channels).Error; err != nil {
		return err
	}
	for _, ch := range channels {
		var accounts []*db.Account
		database.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", ch.ID).Find(&accounts)
		setPool(ch.ID.String(), accounts)
	}
	return nil
}
