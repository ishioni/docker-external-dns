package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ishioni/dexd/internal/config"
	"github.com/ishioni/dexd/internal/controller"
	appmetrics "github.com/ishioni/dexd/internal/metrics"
	"github.com/ishioni/dexd/internal/provider/unifi"
	"github.com/ishioni/dexd/internal/source"
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

	slog.Info("dexd starting",
		"version", Version,
		"gitsha", Gitsha,
		"owner_id", cfg.OwnerID,
		"txt_prefix", cfg.TxtPrefix,
		"unifi_host", cfg.UnifiHost,
		"unifi_site", cfg.UnifiSite,
		"default_target", cfg.DefaultTarget,
		"default_ttl", cfg.DefaultTTL.String(),
		"policy", cfg.Policy,
		"docker_host", cfg.DockerHost,
		"reconcile_interval", cfg.ReconcileInterval,
		"metrics_addr", cfg.MetricsAddr,
		"dry_run", cfg.DryRun,
	)
	appmetrics.SetBuildInfo(Version, Gitsha, cfg.Policy)

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
		int(cfg.DefaultTTL),
	)

	ctrl := controller.New(dockerAdapter{dockerSrc}, unifiClient, cfg.OwnerID, cfg.TxtPrefix, cfg.Policy, cfg.ReconcileInterval)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	metricsServer := startMetricsServer(cfg.MetricsAddr)

	ctrl.Run(ctx)

	if metricsServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("metrics server shutdown failed", "err", err)
		}
	}
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

func startMetricsServer(addr string) *http.Server {
	if addr == "" {
		slog.Info("metrics server disabled")
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", appmetrics.Handler())
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("metrics server listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server failed", "err", err)
		}
	}()
	return server
}
