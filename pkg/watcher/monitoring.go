package watcher

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// admissionCheckStatus tracks the current admission status for each check (1 = admitting, 0 = denying)
	admissionCheckStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kueue_external_admission_check_status",
			Help: "Current admission status for each admission check (1 = admitting, 0 = denying, -1 = error)",
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

	// workloadAdmissionDecisions counts total workload admission decisions
	workloadAdmissionDecisions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kueue_external_admission_workload_decisions_total",
			Help: "Total number of workload admission decisions",
		},
		[]string{"decision", "checks_count"},
	)

	// workloadAdmissionDuration tracks the duration of complete workload admission evaluation
	workloadAdmissionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kueue_external_admission_workload_duration_seconds",
			Help:    "Duration of complete workload admission evaluation in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"checks_count"},
	)
)

func init() {
	// Register metrics with controller-runtime's default registry
	metrics.Registry.MustRegister(
		admissionCheckStatus,
		admissionCheckDecisionsTotal,
		admissionCheckDuration,
		admissionCheckErrors,
		workloadAdmissionDecisions,
		workloadAdmissionDuration,
	)
}

// RecordAdmissionDecision records metrics for an admission decision
func RecordAdmissionDecision(checkName string, admitted bool, duration time.Duration) {
	// Update current status
	if admitted {
		admissionCheckStatus.WithLabelValues(checkName).Set(1)
		admissionCheckDecisionsTotal.WithLabelValues(checkName, "admitted").Inc()
	} else {
		admissionCheckStatus.WithLabelValues(checkName).Set(0)
		admissionCheckDecisionsTotal.WithLabelValues(checkName, "denied").Inc()
	}

	// Record duration
	admissionCheckDuration.WithLabelValues(checkName).Observe(duration.Seconds())
}

// RecordAdmissionError records metrics for admission check errors
func RecordAdmissionError(checkName, errorType string) {
	admissionCheckErrors.WithLabelValues(checkName, errorType).Inc()
	// Set status to error (-1) when there's an error
	admissionCheckStatus.WithLabelValues(checkName).Set(-1)
}

// RecordWorkloadAdmissionDecision records metrics for the overall workload admission decision
func RecordWorkloadAdmissionDecision(ctx context.Context, checkNames []string, finalDecision bool, totalDuration time.Duration) {
	decision := "denied"
	if finalDecision {
		decision = "admitted"
	}

	checksCount := strconv.Itoa(len(checkNames))
	workloadAdmissionDecisions.WithLabelValues(decision, checksCount).Inc()
	workloadAdmissionDuration.WithLabelValues(checksCount).Observe(totalDuration.Seconds())
}

// AdmissionMetrics provides a wrapper for recording admission metrics
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

// RecordDecision records the final admission decision and duration
func (m *AdmissionMetrics) RecordDecision(admitted bool) {
	duration := time.Since(m.startTime)
	RecordAdmissionDecision(m.checkName, admitted, duration)
}

// RecordError records an error during admission evaluation
func (m *AdmissionMetrics) RecordError(errorType string) {
	RecordAdmissionError(m.checkName, errorType)
}
