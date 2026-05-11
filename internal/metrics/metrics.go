package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ishioni/dexd/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "dexd"

var (
	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reconcile_total",
			Help:      "Total number of reconcile attempts by result.",
		},
		[]string{"result"},
	)

	reconcileDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of reconcile attempts.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
	)

	reconcileLastSuccess = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "reconcile_last_success_timestamp_seconds",
			Help:      "Unix timestamp of the last reconcile without errors.",
		},
	)

	reconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reconcile_errors_total",
			Help:      "Total number of reconcile errors by stage.",
		},
		[]string{"stage"},
	)

	changesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "changes_total",
			Help:      "Total number of DNS changes by operation, record type, and result.",
		},
		[]string{"operation", "record_type", "result"},
	)

	planDesiredRecords = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "plan_desired_records",
			Help:      "Number of desired DNS endpoints from the latest reconcile.",
		},
	)

	planCurrentRecords = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "plan_current_records",
			Help:      "Number of provider DNS records from the latest reconcile.",
		},
	)

	planChanges = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "plan_changes",
			Help:      "Number of planned changes from the latest reconcile by operation after policy filtering.",
		},
		[]string{"operation"},
	)

	sourceEndpoints = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "source_endpoints",
			Help:      "Number of desired endpoints discovered from the source during the latest reconcile.",
		},
	)

	sourceErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "source_errors_total",
			Help:      "Total number of source errors by operation.",
		},
		[]string{"operation"},
	)

	dockerEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "docker_events_total",
			Help:      "Total number of Docker/source events by action.",
		},
		[]string{"action"},
	)

	providerRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "provider_requests_total",
			Help:      "Total number of provider HTTP requests by method, result, and status code.",
		},
		[]string{"method", "result", "status_code"},
	)

	providerRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "provider_request_duration_seconds",
			Help:      "Duration of provider HTTP requests by method.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15},
		},
		[]string{"method"},
	)

	providerErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "provider_errors_total",
			Help:      "Total number of provider errors by type.",
		},
		[]string{"type"},
	)

	buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "build_info",
			Help:      "Build and configuration information for dexd.",
		},
		[]string{"version", "gitsha", "policy", "provider"},
	)
)

func init() {
	prometheus.MustRegister(
		reconcileTotal,
		reconcileDuration,
		reconcileLastSuccess,
		reconcileErrorsTotal,
		changesTotal,
		planDesiredRecords,
		planCurrentRecords,
		planChanges,
		sourceEndpoints,
		sourceErrorsTotal,
		dockerEventsTotal,
		providerRequestsTotal,
		providerRequestDuration,
		providerErrorsTotal,
		buildInfo,
	)
}

func Handler() http.Handler {
	return promhttp.Handler()
}

func SetBuildInfo(version, gitsha string, policy config.Policy) {
	buildInfo.WithLabelValues(version, gitsha, string(policy), "unifi").Set(1)
}

func ObserveReconcile(duration time.Duration, success bool) {
	reconcileDuration.Observe(duration.Seconds())
	if success {
		reconcileTotal.WithLabelValues("success").Inc()
		reconcileLastSuccess.Set(float64(time.Now().Unix()))
		return
	}
	reconcileTotal.WithLabelValues("error").Inc()
}

func IncReconcileError(stage string) {
	reconcileErrorsTotal.WithLabelValues(stage).Inc()
}

func SetPlanMetrics(desired, current int, changes map[string]int) {
	planDesiredRecords.Set(float64(desired))
	planCurrentRecords.Set(float64(current))
	sourceEndpoints.Set(float64(desired))

	for _, operation := range []string{"create", "update", "replace", "delete", "orphan_txt_delete"} {
		planChanges.WithLabelValues(operation).Set(float64(changes[operation]))
	}
}

func ObserveChange(operation, recordType string, success bool) {
	result := "success"
	if !success {
		result = "error"
	}
	changesTotal.WithLabelValues(operation, recordType, result).Inc()
}

func IncSourceError(operation string) {
	sourceErrorsTotal.WithLabelValues(operation).Inc()
}

func IncDockerEvent(action string) {
	dockerEventsTotal.WithLabelValues(action).Inc()
}

func ObserveProviderRequest(method string, statusCode int, duration time.Duration, errType string) {
	result := "success"
	status := strconv.Itoa(statusCode)
	if errType != "" {
		result = "error"
		if statusCode == 0 {
			status = "network"
		}
		providerErrorsTotal.WithLabelValues(errType).Inc()
	}

	providerRequestsTotal.WithLabelValues(method, result, status).Inc()
	providerRequestDuration.WithLabelValues(method).Observe(duration.Seconds())
}
