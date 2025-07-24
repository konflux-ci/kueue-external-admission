package watcher

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// admissionCheckStatus tracks the current admission status for each check (1 = admitting, 0 = denying)
	admissionCheckStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kueue_external_admission_check_status",
			Help: "Current admission status for each admission check (1 = admitting, 0 = denying)",
		},
		[]string{"check_name"},
	)

	// admissionCheckDecisionsTotal counts total admission decisions per check
	admissionCheckDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kueue_external_admission_check_decisions_total",
			Help: "Total number of admission decisions made by each admission check",
		},
		[]string{"check_name", "decision"},
	)

	// admissionCheckDuration tracks the duration of admission check evaluations
	admissionCheckDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kueue_external_admission_check_duration_seconds",
			Help:    "Duration of admission check evaluations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"check_name"},
	)

	// admissionCheckErrors counts errors during admission check evaluations
	admissionCheckErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kueue_external_admission_check_errors_total",
			Help: "Total number of errors during admission check evaluations",
		},
		[]string{"check_name", "error_type"},
	)
)

func init() {
	// Register metrics with controller-runtime's default registry
	metrics.Registry.MustRegister(
		admissionCheckStatus,
		admissionCheckDecisionsTotal,
		admissionCheckDuration,
		admissionCheckErrors,
	)
}

// AdmissionMetrics provides a scoped metrics recorder for an admission check
type AdmissionMetrics struct {
	checkName string
	startTime time.Time
}

// NewAdmissionMetrics creates a new metrics recorder for an admission check
func NewAdmissionMetrics(checkName string) *AdmissionMetrics {
	return &AdmissionMetrics{
		checkName: checkName,
		startTime: time.Now(),
	}
}

// RecordDecision records the final admission decision with automatic duration calculation
func (m *AdmissionMetrics) RecordDecision(admitted bool) {
	duration := time.Since(m.startTime)

	// Update current status
	if admitted {
		admissionCheckStatus.WithLabelValues(m.checkName).Set(1)
		admissionCheckDecisionsTotal.WithLabelValues(m.checkName, "admitted").Inc()
	} else {
		admissionCheckStatus.WithLabelValues(m.checkName).Set(0)
		admissionCheckDecisionsTotal.WithLabelValues(m.checkName, "denied").Inc()
	}

	// Record duration
	admissionCheckDuration.WithLabelValues(m.checkName).Observe(duration.Seconds())
}

// RecordError records an error during admission evaluation
func (m *AdmissionMetrics) RecordError(errorType string) {
	admissionCheckErrors.WithLabelValues(m.checkName, errorType).Inc()
	// Don't update status gauge on error - let it keep the last known admission state
	// Errors are tracked separately in admissionCheckErrors metric
}
