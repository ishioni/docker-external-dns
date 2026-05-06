package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// UniFi
	UnifiHost              string
	UnifiAPIKey            string
	UnifiSite              string
	UnifiInsecureSkipVerify bool

	// Docker
	DockerHost string

	// DNS
	TargetIP          string
	OwnerID           string
	TxtPrefix         string
	ReconcileInterval time.Duration

	// App
	LogLevel  slog.Level
	LogFormat string // "text" or "json"
	DryRun    bool
}

func Load() (*Config, error) {
	cfg := &Config{
		UnifiSite:              getEnvDefault("UNIFI_SITE", "default"),
		UnifiInsecureSkipVerify: parseBool(getEnvDefault("UNIFI_INSECURE_SKIP_VERIFY", "true")),
		DockerHost:             getEnvDefault("DOCKER_HOST", "unix:///var/run/docker.sock"),
		OwnerID:   getEnvDefault("TXT_OWNER", "docker-external-dns"),
		TxtPrefix: getEnvDefault("TXT_PREFIX", ""),
		LogFormat:              getEnvDefault("LOG_FORMAT", "text"),
		DryRun:                 parseBool(getEnvDefault("DRY_RUN", "false")),
	}

	cfg.UnifiHost = os.Getenv("UNIFI_HOST")
	if cfg.UnifiHost == "" {
		return nil, fmt.Errorf("UNIFI_HOST is required")
	}

	cfg.UnifiAPIKey = os.Getenv("UNIFI_API_KEY")
	if cfg.UnifiAPIKey == "" {
		return nil, fmt.Errorf("UNIFI_API_KEY is required")
	}

	cfg.TargetIP = os.Getenv("TARGET_IP")
	if cfg.TargetIP == "" {
		return nil, fmt.Errorf("TARGET_IP is required")
	}

	interval := getEnvDefault("RECONCILE_INTERVAL", "5m")
	d, err := time.ParseDuration(interval)
	if err != nil {
		return nil, fmt.Errorf("invalid RECONCILE_INTERVAL %q: %w", interval, err)
	}
	cfg.ReconcileInterval = d

	level := getEnvDefault("LOG_LEVEL", "info")
	switch level {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		cfg.LogLevel = slog.LevelInfo
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
