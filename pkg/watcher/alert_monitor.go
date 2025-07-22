package watcher

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/util/admissioncheck"
)

// Lister provides a way to list objects that need admission checking
type Lister interface {
	List(context.Context) ([]client.Object, error)
}

// AlertMonitor periodically checks alert states and emits events when changes occur
type AlertMonitor struct {
	admissionService *AdmissionService
	lister           Lister
	period           time.Duration
	logger           logr.Logger
	stopChan         chan struct{}
	running          bool
}

var _ manager.Runnable = &AlertMonitor{}

// NewAlertMonitor creates a new alert monitor
func NewAlertMonitor(
	admissionService *AdmissionService,
	lister Lister,
	period time.Duration,
	logger logr.Logger,
) *AlertMonitor {
	return &AlertMonitor{
		admissionService: admissionService,
		lister:           lister,
		period:           period,
		logger:           logger,
		stopChan:         make(chan struct{}),
	}
}

// Start implements manager.Runnable interface
func (m *AlertMonitor) Start(ctx context.Context) error {
	if m.running {
		return nil
	}

	m.running = true
	m.logger.Info("Starting alert monitor", "period", m.period)

	m.monitorLoop(ctx)
	return nil
}

// Stop stops the alert monitor
func (m *AlertMonitor) Stop() {
	if !m.running {
		return
	}

	m.logger.Info("Stopping alert monitor")
	close(m.stopChan)
	m.running = false
}

// IsRunning returns whether the monitor is currently running
func (m *AlertMonitor) IsRunning() bool {
	return m.running
}

// monitorLoop runs the monitoring loop
func (m *AlertMonitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.period)
	defer ticker.Stop()

	// Track previous state to detect changes
	previousStates := make(map[string]bool) // workloadKey -> shouldAdmit

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Alert monitor stopped due to context cancellation")
			return
		case <-m.stopChan:
			m.logger.Info("Alert monitor stopped")
			return
		case <-ticker.C:
			m.checkAndEmitEvents(ctx, previousStates)
		}
	}
}

// checkAndEmitEvents checks current alert states and emits events for changed workloads
func (m *AlertMonitor) checkAndEmitEvents(ctx context.Context, previousStates map[string]bool) {
	// Get all workloads that need admission checks
	workloads, err := m.lister.List(ctx)
	if err != nil {
		m.logger.Error(err, "Failed to list workloads for alert monitoring")
		return
	}

	currentStates := make(map[string]bool)

	for _, obj := range workloads {
		workload, ok := obj.(*kueue.Workload)
		if !ok {
			continue
		}

		workloadKey := client.ObjectKeyFromObject(workload).String()

		// Get admission checks for this workload (using the controller name constant)
		relevantChecks, err := admissioncheck.FilterForController(
			ctx,
			nil,
			workload.Status.AdmissionChecks,
			"konflux-ci.dev/kueue-external-admission",
		)
		if err != nil {
			m.logger.Error(err, "Failed to filter admission checks", "workload", workload.Name)
			continue
		}

		if len(relevantChecks) == 0 {
			continue
		}

		// Check current admission state
		result := m.admissionService.ShouldAdmitWorkload(ctx, relevantChecks)
		currentState := result.ShouldAdmit()
		currentStates[workloadKey] = currentState

		// Compare with previous state and emit event if changed
		if prevState, existed := previousStates[workloadKey]; !existed || prevState != currentState {
			m.logger.Info("Alert state changed for workload, emitting reconcile event",
				"workload", workload.Name,
				"namespace", workload.Namespace,
				"previousState", prevState,
				"currentState", currentState)

			// Emit generic event to trigger workload reconciliation
			select {
			case m.admissionService.eventsChannel <- event.GenericEvent{Object: workload}:
				// Event sent successfully
			default:
				// Channel full, log warning but continue
				m.logger.V(1).Info("Events channel full, skipping event", "workload", workload.Name)
			}
		}
	}

	// Update previous states for next iteration
	for k, v := range currentStates {
		previousStates[k] = v
	}

	// Clean up states for workloads that no longer exist
	for k := range previousStates {
		if _, exists := currentStates[k]; !exists {
			delete(previousStates, k)
		}
	}
}
