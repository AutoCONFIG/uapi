package appsettings

import (
	"strconv"

	"github.com/AutoCONFIG/uapi/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AdminUsername           = "admin.username"
	AdminPasswordHash       = "admin.password_hash"
	LogRetentionDays        = "logging.retention_days"
	RedeemCodeRetentionDays = "logging.redeem_code_retention_days"
	ModelRatios             = "billing.model_ratios"
	UIBackground            = "ui.background"
	UIPublicBaseURL         = "ui.public_base_url"
	UIWallpaperPath         = "ui.wallpaper_path"
	UserMaxKeysPerUser      = "user.max_keys_per_user"
)

func Bootstrap(database *gorm.DB) error {
	defaults := map[string]string{
		AdminUsername:           "admin",
		LogRetentionDays:        "180",
		RedeemCodeRetentionDays: "180",
		ModelRatios:             "{}",
		UIBackground:            "mesh",
		UserMaxKeysPerUser:      "1",
	}
	return database.Transaction(func(tx *gorm.DB) error {
		for key, value := range defaults {
			setting := db.SystemSetting{Key: key, Value: value}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&setting).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func Get(database *gorm.DB, key, fallback string) string {
	if database == nil {
		return fallback
	}
	var setting db.SystemSetting
	if err := database.First(&setting, "key = ?", key).Error; err != nil {
		return fallback
	}
	return setting.Value
}

func GetInt(database *gorm.DB, key string, fallback int) int {
	value := Get(database, key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func Set(database *gorm.DB, key, value string) error {
	return database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&db.SystemSetting{Key: key, Value: value}).Error
}

func SetMany(database *gorm.DB, values map[string]string) error {
	return database.Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			if err := Set(tx, key, value); err != nil {
				return err
			}
		}
		return nil
	})
}
