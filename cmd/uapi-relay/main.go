package main

import (
	"flag"
	"os"
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/debugdump"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relayserver"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file path")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}
	logger.Configure(cfg.Logging.Level)
	log := logger.Component("uapi-relay")
	if err := config.ValidateRelay(cfg); err != nil {
		log.Error("relay config invalid", logger.Err(err))
		os.Exit(1)
	}
	antigravity.StartVersionUpdater(nil)

	if err := crypto.Init(cfg.Security.EncryptionKey); err != nil {
		log.Error("init crypto failed", logger.Err(err))
		os.Exit(1)
	}
	debugdump.Configure(debugdump.Config{Enabled: cfg.DebugDump.Enabled && cfg.DebugDump.Mode == "local", Dir: cfg.DebugDump.Dir, QueueMaxSize: cfg.DebugDump.QueueMaxItems})
	uploadTimeout, _ := time.ParseDuration(cfg.DebugDump.UploadTimeout)
	relay.ConfigureRelayDebugDump(relay.RelayDebugDumpConfig{
		Enabled:        cfg.DebugDump.Enabled,
		Mode:           cfg.DebugDump.Mode,
		Dir:            cfg.DebugDump.Dir,
		MaxEntries:     cfg.DebugDump.MaxEntries,
		ControlURL:     cfg.Gateway.ControlURL,
		RelayNodeID:    cfg.Gateway.RelayNodeID,
		InternalSecret: cfg.Gateway.InternalSecret,
		QueueMaxItems:  cfg.DebugDump.QueueMaxItems,
		BatchMaxBytes:  int64(cfg.DebugDump.BatchMaxBytesMB) * 1024 * 1024,
		UploadTimeout:  uploadTimeout,
	})
	if cfg.DebugDump.Enabled && cfg.DebugDump.Mode == "local" {
		oauthdebug.Configure(cfg.DebugDump.Dir)
	} else {
		oauthdebug.Configure("")
	}

	pools := relay.NewPoolManager()
	affinity := relay.NewAffinityCache()
	concLimiter := relay.NewConcurrencyLimiter(cfg.Server.ConcurrencyLimit)
	relayer := relay.NewRelayer(nil, pools, nil, affinity, cfg.Server.ConcurrencyLimit, cfg.Gateway.InternalSecret, cfg.Gateway.RequireInternal, cfg.Gateway.ControlURL, relay.WithConcurrencyLimiter(concLimiter), relay.WithTrustedProxies(cfg.Security.TrustedProxies), relay.WithStreamIdleTimeout(time.Duration(cfg.Server.StreamIdleTimeoutSeconds)*time.Second))
	relayer.SetLargePayloadThreshold(appsettings.GetInt(nil, appsettings.LargePayloadThresholdMB, 256))
	pullInterval, _ := time.ParseDuration(cfg.Gateway.ConfigPullInterval)
	relayer.StartConfigPuller(cfg.Gateway.RelayNodeID, pullInterval)

	srv := relayserver.New(cfg, relayer, cfg.Gateway.RelayNodeID)
	log.Info("uapi relay ready")
	if err := srv.Start(); err != nil {
		log.Error("server error", logger.Err(err))
		os.Exit(1)
	}
}
