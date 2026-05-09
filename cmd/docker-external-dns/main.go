package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ishioni/docker-external-dns/internal/config"
	"github.com/ishioni/docker-external-dns/internal/controller"
	"github.com/ishioni/docker-external-dns/internal/provider/unifi"
	"github.com/ishioni/docker-external-dns/internal/source"
)

// Build-time metadata populated by -ldflags "-X main.Version=… -X main.Gitsha=…".
var (
	Version = "dev"
	Gitsha  = "dev"
)

// dockerAdapter wraps a *source.DockerSource so it satisfies controller.Source,
// translating Docker SDK events into the controller's domain Event type.
type dockerAdapter struct {
	*source.DockerSource
}

func (a dockerAdapter) Events(ctx context.Context) (<-chan controller.Event, <-chan error) {
	raw, errCh := a.DockerSource.Events(ctx)
	out := make(chan controller.Event)
	go func() {
		defer close(out)
		for m := range raw {
			ev := controller.Event{Action: string(m.Action), Name: m.Actor.Attributes["name"]}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errCh
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	setupLogging(cfg)

	slog.Info("docker-external-dns starting",
		"version", Version,
		"gitsha", Gitsha,
		"owner_id", cfg.OwnerID,
		"txt_prefix", cfg.TxtPrefix,
		"unifi_host", cfg.UnifiHost,
		"unifi_site", cfg.UnifiSite,
		"default_target", cfg.DefaultTarget,
		"policy", cfg.Policy,
		"docker_host", cfg.DockerHost,
		"reconcile_interval", cfg.ReconcileInterval,
		"dry_run", cfg.DryRun,
	)

	dockerSrc, err := source.NewDockerSource(cfg.DockerHost, cfg.DefaultTarget, cfg.OwnerID)
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

	ctrl := controller.New(dockerAdapter{dockerSrc}, unifiClient, cfg.OwnerID, cfg.TxtPrefix, cfg.Policy, cfg.ReconcileInterval)

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
