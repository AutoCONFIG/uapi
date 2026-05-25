package main

import (
	"flag"
	"os"
	"time"

	"github.com/AutoCONFIG/uapi/internal/admin"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/server"
	"github.com/AutoCONFIG/uapi/internal/user"
	"gorm.io/gorm"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file path")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}
	logger.Configure(cfg.Logging.Level)
	log := logger.Component("main")
	antigravity.StartVersionUpdater(nil)

	if err := crypto.Init(cfg.Security.EncryptionKey); err != nil {
		log.Error("init crypto failed", logger.Err(err))
		os.Exit(1)
	}

	pools := relay.NewPoolManager()
	var database *gorm.DB
	var billing *relay.BillingService
	var userSvc *user.Service

	if cfg.Server.Mode != "relay" {
		var err error
		database, err = db.Init(cfg.Database.DSN())
		if err != nil {
			log.Error("init database failed", logger.Err(err))
			os.Exit(1)
		}
		log.Info("database connected")

		billing = relay.NewBillingService(database)

		// Load account pools from DB for local/all-in-one relay fallback.
		if err := admin.InitPools(database, func(channelID string, accounts []*db.Account) {
			pools.SetPool(channelID, relay.NewAccountPool(accounts))
		}); err != nil {
			log.Warn("init pools failed", logger.Err(err))
		}
		log.Info("account pools loaded")

		admin.StartLogCleanup(database, cfg)

		accessTokenExpiry := 15 * time.Minute
		if cfg.Auth.AccessTokenExpiry != "" {
			if d, err := time.ParseDuration(cfg.Auth.AccessTokenExpiry); err == nil {
				accessTokenExpiry = d
			}
		}
		refreshTokenExpiry := 720 * time.Hour
		if cfg.Auth.RefreshTokenExpiry != "" {
			if d, err := time.ParseDuration(cfg.Auth.RefreshTokenExpiry); err == nil {
				refreshTokenExpiry = d
			}
		}
		userSvc = user.NewService(database, cfg.Security.JWTSecret, accessTokenExpiry, refreshTokenExpiry, cfg.User.MaxKeysPerUser)
	}

	srv := server.New(cfg, database, pools, billing, userSvc, *configPath)
	log.Info("uapi ready")
	if err := srv.Start(); err != nil {
		log.Error("server error", logger.Err(err))
		os.Exit(1)
	}
}
