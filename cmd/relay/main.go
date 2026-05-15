package main

import (
	"flag"
	"log"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/admin"
	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/crypto"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay"
	"github.com/AutoCONFIG/cli-relay/internal/server"
	"github.com/AutoCONFIG/cli-relay/internal/user"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file path")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := crypto.Init(cfg.Security.EncryptionKey); err != nil {
		log.Fatalf("init crypto: %v", err)
	}

	database, err := db.Init(cfg.Database.DSN())
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	log.Println("database connected")

	pools := relay.NewPoolManager()
	billing := relay.NewBillingService(database)

	// Load account pools from DB
	if err := admin.InitPools(database, func(channelID string, accounts []*db.Account) {
		pools.SetPool(channelID, relay.NewAccountPool(accounts))
	}); err != nil {
		log.Printf("warning: init pools: %v", err)
	}
	log.Println("account pools loaded")

	// Start background log cleanup
	admin.StartLogCleanup(database, cfg.Logging.RetentionDays)

	// Initialize user service
	jwtExpiry := 24 * time.Hour
	if cfg.User.JWTExpiry != "" {
		if d, err := time.ParseDuration(cfg.User.JWTExpiry); err == nil {
			jwtExpiry = d
		}
	}
	userSvc := user.NewService(database, cfg.Security.JWTSecret, jwtExpiry)

	srv := server.New(cfg, database, pools, billing, userSvc, *configPath)
	log.Println("cli-relay ready")
	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
