package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsNonPositiveReconcileInterval(t *testing.T) {
	tests := []string{"0s", "-1s"}
	for _, interval := range tests {
		t.Run(interval, func(t *testing.T) {
			t.Setenv("UNIFI_HOST", "https://unifi.example.com")
			t.Setenv("UNIFI_API_KEY", "secret")
			t.Setenv("DEFAULT_TARGET", "10.0.0.1")
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
