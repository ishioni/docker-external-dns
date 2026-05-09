package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Policy string

const (
	PolicySync       Policy = "sync"
	PolicyUpsertOnly Policy = "upsert-only"
	PolicyCreateOnly Policy = "create-only"
)

type Config struct {
	// UniFi
	UnifiHost               string `env:"UNIFI_HOST,required"`
	UnifiAPIKey             string `env:"UNIFI_API_KEY,required"`
	UnifiSite               string `env:"UNIFI_SITE" envDefault:"default"`
	UnifiInsecureSkipVerify bool   `env:"UNIFI_INSECURE_SKIP_VERIFY" envDefault:"true"`

	// Docker
	DockerHost string `env:"DOCKER_HOST" envDefault:"unix:///var/run/docker.sock"`

	// DNS
	DefaultTarget     string        `env:"DEFAULT_TARGET,required"`
	OwnerID           string        `env:"TXT_OWNER" envDefault:"docker-external-dns"`
	TxtPrefix         string        `env:"TXT_PREFIX" envDefault:""`
	Policy            Policy        `env:"POLICY" envDefault:"sync"`
	DefaultTTL        DefaultTTL    `env:"DEFAULT_TTL" envDefault:"auto"`
	ReconcileInterval time.Duration `env:"RECONCILE_INTERVAL" envDefault:"5m"`

	// App
	LogLevel    slog.Level `env:"-"`
	LogLevelRaw string     `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat   string     `env:"LOG_FORMAT" envDefault:"text"` // "text" or "json"
	DryRun      bool       `env:"DRY_RUN" envDefault:"false"`
	MetricsAddr string     `env:"METRICS_ADDR"`
}

type DefaultTTL int

func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	if _, ok := os.LookupEnv("METRICS_ADDR"); !ok {
		cfg.MetricsAddr = ":8080"
	}

	if cfg.ReconcileInterval <= 0 {
		return nil, fmt.Errorf("RECONCILE_INTERVAL must be positive, got %q", cfg.ReconcileInterval)
	}

	level := strings.ToLower(strings.TrimSpace(cfg.LogLevelRaw))
	switch level {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		if level != "info" {
			return nil, fmt.Errorf("invalid LOG_LEVEL %q: must be one of %q, %q, %q, %q", level, "debug", "info", "warn", "error")
		}
		cfg.LogLevel = slog.LevelInfo
	}

	return &cfg, nil
}

func (p *Policy) UnmarshalText(text []byte) error {
	policy := Policy(strings.ToLower(strings.TrimSpace(string(text))))
	if err := policy.Validate(); err != nil {
		return err
	}
	*p = policy
	return nil
}

func (p Policy) Validate() error {
	switch p {
	case PolicySync, PolicyUpsertOnly, PolicyCreateOnly:
		return nil
	default:
		return fmt.Errorf("invalid POLICY %q: must be one of %q, %q, %q", p, PolicySync, PolicyUpsertOnly, PolicyCreateOnly)
	}
}

func (t *DefaultTTL) UnmarshalText(text []byte) error {
	raw := strings.ToLower(strings.TrimSpace(string(text)))
	if raw == "" || raw == "auto" {
		*t = 0
		return nil
	}

	ttl, err := strconv.Atoi(raw)
	if err != nil || ttl <= 0 {
		return fmt.Errorf("invalid DEFAULT_TTL %q: must be %q or a positive integer", string(text), "auto")
	}
	*t = DefaultTTL(ttl)
	return nil
}

func (t DefaultTTL) String() string {
	if t == 0 {
		return "auto"
	}
	return strconv.Itoa(int(t))
}
