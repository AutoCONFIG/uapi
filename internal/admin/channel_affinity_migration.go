package admin

import (
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/db"
	"gorm.io/gorm"
)

const defaultChannelAffinityMigrationKey = "migration.channel_default_affinity_ttl.v1"
const defaultAllChannelAffinityMigrationKey = "migration.channel_default_affinity_ttl.v2"

// EnsureDefaultChannelAffinityTTL backfills the affinity default for existing channels once.
// Admins can later set affinity_ttl to 0 to explicitly disable affinity without
// startup code changing it again.
func EnsureDefaultChannelAffinityTTL(database *gorm.DB) error {
	if database == nil {
		return nil
	}
	if appsettings.Get(database, defaultAllChannelAffinityMigrationKey, "") == "done" {
		return nil
	}
	formats := defaultAffinityAPIFormats()
	return database.Transaction(func(tx *gorm.DB) error {
		if appsettings.Get(tx, defaultChannelAffinityMigrationKey, "") != "done" {
			if err := tx.Model(&db.Channel{}).
				Where("deleted_at IS NULL").
				Where("affinity_ttl = 0").
				Where("api_format IN ?", formats).
				Updates(map[string]interface{}{
					"affinity_ttl": DefaultOAuthChannelAffinityTTL,
					"updated_at":   time.Now(),
				}).Error; err != nil {
				return err
			}
			if err := appsettings.Set(tx, defaultChannelAffinityMigrationKey, "done"); err != nil {
				return err
			}
		}
		if err := tx.Model(&db.Channel{}).
			Where("deleted_at IS NULL").
			Where("affinity_ttl = 0").
			Updates(map[string]interface{}{
				"affinity_ttl": DefaultOAuthChannelAffinityTTL,
				"updated_at":   time.Now(),
			}).Error; err != nil {
			return err
		}
		return appsettings.Set(tx, defaultAllChannelAffinityMigrationKey, "done")
	})
}

func defaultAffinityAPIFormats() []string {
	return []string{"codex", "gemini_code", "claude_code", "antigravity"}
}
