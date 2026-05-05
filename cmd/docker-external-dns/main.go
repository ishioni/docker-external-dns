package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/movishell/docker-external-dns/internal/config"
	"github.com/movishell/docker-external-dns/internal/controller"
	"github.com/movishell/docker-external-dns/internal/provider/unifi"
	"github.com/movishell/docker-external-dns/internal/source"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	setupLogging(cfg)

	slog.Info("docker-external-dns starting",
		"owner_id", cfg.OwnerID,
		"unifi_host", cfg.UnifiHost,
		"unifi_site", cfg.UnifiSite,
		"target_ip", cfg.TargetIP,
		"docker_host", cfg.DockerHost,
		"reconcile_interval", cfg.ReconcileInterval,
		"dry_run", cfg.DryRun,
	)

	dockerSrc, err := source.NewDockerSource(cfg.DockerHost, cfg.TargetIP, cfg.OwnerID)
	if err != nil {
		slog.Error("failed to create docker client", "err", err)
		os.Exit(1)
	}
	defer dockerSrc.Close()

	unifiClient := unifi.NewClient(
		cfg.UnifiHost,
		cfg.UnifiAPIKey,
		cfg.UnifiSite,
		cfg.UnifiInsecureSkipVerify,
		cfg.DryRun,
	)

	ctrl := controller.New(dockerSrc, unifiClient, cfg.OwnerID, cfg.ReconcileInterval)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctrl.Run(ctx)
	slog.Info("shutdown complete")
}

func setupLogging(cfg *config.Config) {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}
