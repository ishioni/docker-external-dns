package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ishioni/docker-external-dns/internal/config"
)

func TestHandlerExportsMetrics(t *testing.T) {
	SetBuildInfo("test-version", "test-sha", config.PolicySync)
	IncDockerEvent("start")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "docker_external_dns_build_info") {
		t.Fatalf("metrics body missing build_info: %s", body)
	}
	if !strings.Contains(body, "docker_external_dns_docker_events_total") {
		t.Fatalf("metrics body missing docker_events_total: %s", body)
	}
}
