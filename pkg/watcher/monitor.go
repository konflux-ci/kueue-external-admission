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
	"sigs.k8s.io/kueue/pkg/workload"

	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
)

// Lister provides a way to list objects that need admission checking
type Lister interface {
	List(context.Context) ([]client.Object, error)
}

// Monitor periodically checks alert states and emits events when changes occur
type Monitor struct {
	admissionService *AdmissionService
	lister           Lister
	client           client.Client
	period           time.Duration
	logger           logr.Logger
}

var _ manager.Runnable = &Monitor{}

// NewMonitor creates a new alert monitor
func NewMonitor(
	admissionService *AdmissionService,
	lister Lister,
	client client.Client,
	period time.Duration,
	logger logr.Logger,
) *Monitor {
	return &Monitor{
		admissionService: admissionService,
		lister:           lister,
		client:           client,
		period:           period,
		logger:           logger,
	}
}

// Start implements manager.Runnable interface
func (m *Monitor) Start(ctx context.Context) error {
	m.logger.Info("Starting monitor", "period", m.period)
	m.monitorLoop(ctx)
	return nil
}

// monitorLoop runs the monitoring loop
func (m *Monitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.period)
	defer ticker.Stop()

	// Track previous state to detect changes
	previousStates := make(map[string]bool) // workloadKey -> shouldAdmit

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Monitor stopped due to context cancellation")
			return
		case <-ticker.C:
			m.checkAndEmitEvents(ctx, previousStates)
		}
	}
}

// checkAndEmitEvents checks current alert states and emits events for changed workloads
func (m *Monitor) checkAndEmitEvents(ctx context.Context, previousStates map[string]bool) {
	m.logger.Info("Periodically checking and emitting events")
	// Get all workloads that need admission checks
	workloads, err := m.lister.List(ctx)
	if err != nil {
		m.logger.Error(err, "Failed to list workloads")
		return
	}

	currentStates := make(map[string]bool)

	for _, obj := range workloads {
		wl, ok := obj.(*kueue.Workload)
		if !ok {
			continue
		}

		if workload.IsAdmitted(wl) {
			continue
		}

		workloadKey := client.ObjectKeyFromObject(wl).String()

		// Get admission checks for this workload (using the controller name constant)
		relevantChecks, err := admissioncheck.FilterForController(
			ctx,
			m.client,
			wl.Status.AdmissionChecks,
			constant.ControllerName,
		)
		if err != nil {
			m.logger.Error(err, "Failed to filter admission checks", "workload", wl.Name)
			continue
		}

		if len(relevantChecks) == 0 {
			continue
		}

		// Check admission using the same mechanism as the workload controller
		admissionResult, err := m.admissionService.ShouldAdmitWorkload(ctx, relevantChecks)
		if err != nil {
			m.logger.Error(err, "Failed to check admission state", "workload", wl.Name)
			continue
		}

		currentState := admissionResult.ShouldAdmit()
		currentStates[workloadKey] = currentState

		// Compare with previous state and emit event if changed
		if prevState, existed := previousStates[workloadKey]; !existed || prevState != currentState {
			m.logger.Info("Admission state changed for workload, emitting reconcile event",
				"workload", wl.Name,
				"namespace", wl.Namespace,
				"previousState", prevState,
				"currentState", currentState)

			// Emit generic event to trigger workload reconciliation
			select {
			case m.admissionService.eventsChannel <- event.GenericEvent{Object: wl}:
				// Event sent successfully
			default:
				// Channel full, log warning but continue
				m.logger.V(1).Info("Events channel full, skipping event", "workload", wl.Name)
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
