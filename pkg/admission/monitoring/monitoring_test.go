package monitoring

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestAdmissionMetrics_RecordSyncFailure(t *testing.T) {
	RegisterTestingT(t)

	checkName := "test-check"
	metrics := NewAdmissionMetrics(checkName)

	// Get initial counter value
	initialValue := getCounterValue(admissionCheckSyncFailures, checkName)

	// Record a sync failure
	metrics.RecordSyncFailure()

	// Verify the counter was incremented
	newValue := getCounterValue(admissionCheckSyncFailures, checkName)
	Expect(newValue).To(Equal(initialValue+1), "Expected sync failure counter to increment")

	// Record another sync failure
	metrics.RecordSyncFailure()

	// Verify the counter was incremented again
	finalValue := getCounterValue(admissionCheckSyncFailures, checkName)
	Expect(finalValue).To(Equal(initialValue+2), "Expected sync failure counter to increment again")
}

// Helper function to get the current value of a counter metric
func getCounterValue(counter *prometheus.CounterVec, checkName string) float64 {
	metric := &dto.Metric{}
	err := counter.WithLabelValues(checkName).Write(metric)
	if err != nil {
		return 0
	}
	if metric.Counter == nil {
		return 0
	}
	return metric.Counter.GetValue()
}
