package main

import (
	"flag"
	"os"
	"time"

	"github.com/AutoCONFIG/uapi/internal/admin"
	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/server"
	"github.com/AutoCONFIG/uapi/internal/user"
	"github.com/AutoCONFIG/uapi/internal/webstatic"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file path")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}
	logger.Configure(cfg.Logging.Level)
	log := logger.Component("uapi-gateway")
	antigravity.StartVersionUpdater(nil)

	if err := crypto.Init(cfg.Security.EncryptionKey); err != nil {
		log.Error("init crypto failed", logger.Err(err))
		os.Exit(1)
	}
	debugdump.Configure(debugdump.Config{Enabled: cfg.DebugDump.Enabled && cfg.DebugDump.Mode == "local", Dir: cfg.DebugDump.Dir, QueueMaxSize: cfg.DebugDump.QueueMaxItems})
	relay.ConfigureRelayDebugDump(relay.RelayDebugDumpConfig{
		Enabled:    cfg.DebugDump.Enabled,
		Mode:       cfg.DebugDump.Mode,
		Dir:        cfg.DebugDump.Dir,
		MaxEntries: cfg.DebugDump.MaxEntries,
	})
	if cfg.DebugDump.Enabled && cfg.DebugDump.Mode == "local" {
		oauthdebug.Configure(cfg.DebugDump.Dir)
	} else {
		oauthdebug.Configure("")
	}
	database, err := db.Init(cfg.Database.DSN())
	if err != nil {
		log.Error("init database failed", logger.Err(err))
		os.Exit(1)
	}
	log.Info("database connected")
	if err := appsettings.Bootstrap(database); err != nil {
		log.Error("init system settings failed", logger.Err(err))
		os.Exit(1)
	}
	if err := admin.EnsureDefaultChannelAffinityTTL(database); err != nil {
		log.Error("init channel affinity defaults failed", logger.Err(err))
		os.Exit(1)
	}

	pools := relay.NewPoolManager()
	billing := relay.NewBillingService(database)
	if err := admin.InitPools(database, func(channelID string, accounts []*db.Account) {
		pools.SetPool(channelID, relay.NewAccountPool(accounts))
	}); err != nil {
		log.Warn("init pools failed", logger.Err(err))
	}
	admin.StartLogCleanup(database)

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
	userSvc := user.NewService(database, cfg.Security.JWTSecret, accessTokenExpiry, refreshTokenExpiry)

	srv := server.NewGateway(cfg, database, pools, billing, userSvc, *configPath, server.WithWebFS(webstatic.FS()))
	log.Info("uapi gateway ready")
	if err := srv.Start(); err != nil {
		log.Error("server error", logger.Err(err))
		os.Exit(1)
	}
}
