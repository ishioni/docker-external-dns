package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadPolicy(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want Policy
	}{
		{name: "default", want: PolicySync},
		{name: "sync", env: "sync", want: PolicySync},
		{name: "upsert-only", env: "upsert-only", want: PolicyUpsertOnly},
		{name: "create-only", env: "create-only", want: PolicyCreateOnly},
		{name: "trim and lowercase", env: " UPSERT-ONLY ", want: PolicyUpsertOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnv(t)
			if tt.env != "" {
				t.Setenv("POLICY", tt.env)
			}

			got, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.Policy != tt.want {
				t.Fatalf("Policy = %q, want %q", got.Policy, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidPolicy(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLICY", "delete-only")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid POLICY") {
		t.Fatalf("Load() error = %q, want invalid policy error", err)
	}
}

func TestLoadDefaultsPolicyAndInterval(t *testing.T) {
	setRequiredEnv(t)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Policy != PolicySync {
		t.Fatalf("Policy = %q, want %q", got.Policy, PolicySync)
	}
	if got.ReconcileInterval != 5*time.Minute {
		t.Fatalf("ReconcileInterval = %s, want 5m", got.ReconcileInterval)
	}
	if got.MetricsAddr != ":8080" {
		t.Fatalf("MetricsAddr = %q, want :8080", got.MetricsAddr)
	}
}

func TestLoadAllowsEmptyMetricsAddr(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("METRICS_ADDR", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.MetricsAddr != "" {
		t.Fatalf("MetricsAddr = %q, want empty", got.MetricsAddr)
	}
}

func TestLoadRejectsNonPositiveReconcileInterval(t *testing.T) {
	tests := []string{"0s", "-1s"}
	for _, interval := range tests {
		t.Run(interval, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("RECONCILE_INTERVAL", interval)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), "RECONCILE_INTERVAL must be positive") {
				t.Fatalf("Load() error = %q, want positive interval error", err)
			}
		})
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("UNIFI_API_KEY", "secret")
	t.Setenv("DEFAULT_TARGET", "10.0.0.1")
}
