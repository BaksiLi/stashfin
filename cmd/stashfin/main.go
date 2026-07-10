package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BaksiLi/stashfin/internal/buildinfo"
	"github.com/BaksiLi/stashfin/internal/config"
	"github.com/BaksiLi/stashfin/internal/jellyfin"
	"github.com/BaksiLi/stashfin/internal/stash"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	logger.Info("starting Stashfin process", "version", buildinfo.Version, "commit", buildinfo.Commit)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	stashClient := stash.NewClient(cfg.StashInternalURL, cfg.StashPublicURL, cfg.StashAPIKey, cfg.StashTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.StashTimeout)
	version, err := stashClient.Check(ctx)
	cancel()
	if err != nil {
		logger.Warn("stash health check failed; server will still start", "error", err)
	} else {
		logger.Info("connected to stash", "version", version)
	}

	app := jellyfin.NewServer(cfg, stashClient, logger)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           jellyfin.LogRequest(logger, app),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("starting Stashfin",
			"version", buildinfo.Version,
			"address", cfg.Address,
			"server_name", cfg.ServerName,
			"stash_internal_url", cfg.StashInternalURL,
			"stash_public_url", cfg.StashPublicURL,
		)
		errs <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signals:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("Stashfin stopped")
}
