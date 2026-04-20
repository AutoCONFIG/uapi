package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/manager"
	codexprovider "github.com/AutoCONFIG/cli-relay/internal/provider/codex"
	"github.com/AutoCONFIG/cli-relay/internal/provider"
	"github.com/AutoCONFIG/cli-relay/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	var logLevel slog.Level
	switch cfg.Log.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Setup database
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Setup admin password from config if set and DB has none
	if cfg.Admin.Password != "" {
		hasAdmin, err := database.HasAdmin()
		if err != nil {
			logger.Error("failed to check admin", "error", err)
			os.Exit(1)
		}
		if !hasAdmin {
			if err := database.SetAdminPassword(cfg.Admin.Password); err != nil {
				logger.Error("failed to set admin password", "error", err)
				os.Exit(1)
			}
			logger.Info("admin password initialized from config")
		}
	}

	// Setup providers
	providerList := make([]provider.Provider, 0)

	for name, pcfg := range cfg.Providers {
		if !pcfg.Enabled {
			continue
		}

		switch name {
		case "codex":
			p := codexprovider.New(codexprovider.Config{
				Issuer:      pcfg.Issuer,
				StoragePath: pcfg.StoragePath,
			})
			providerList = append(providerList, p)
		default:
			logger.Warn("unknown provider, skipping", "provider", name)
		}
	}

	if len(providerList) == 0 {
		logger.Error("no providers enabled")
		os.Exit(1)
	}

	// Create manager
	mgr := manager.NewTokenManager(providerList, database, cfg.Providers, logger)

	// Create and start scheduler
	sched := manager.NewScheduler(mgr, cfg.Refresh, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Start(ctx)

	// Create HTTP server
	srv := server.New(mgr, database, logger)
	httpServer := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start HTTP server
	go func() {
		logger.Info("starting server", "listen", cfg.Server.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	sched.Stop()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	logger.Info("stopped")
}
