// Package enqueue provides functionality for enqueuing workload reconciliation events
// when admission check results change. It acts as a bridge between the admission
// system and the Kubernetes controller runtime, ensuring that workloads are
// re-evaluated when their admission check results change.
package enqueue

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	admissionmanager "github.com/konflux-ci/kueue-external-admission/pkg/admission/manager"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
)

// Enqueuer listens for admission result changes and triggers workload reconciliation
// by enqueuing events for affected workloads. It implements the manager.Runnable
// interface to integrate with the controller-runtime manager.
type Enqueuer struct {
	admissionService *admissionmanager.AdmissionManager // The admission manager to listen for result changes
	eventCh          chan<- event.GenericEvent          // Channel for sending events to trigger workload reconciliation
	client           client.Client                      // Kubernetes client for listing workloads
	logger           logr.Logger                        // Logger for structured logging
}

var _ manager.Runnable = &Enqueuer{}

// NewEnqueuer creates a new Enqueuer instance for handling admission result changes.
//
// Parameters:
//   - admissionService: The admission manager to listen for result changes
//   - eventCh: Channel for sending events to trigger workload reconciliation
//   - client: Kubernetes client for listing workloads
//   - logger: Logger for structured logging
//
// Returns:
//   - *Enqueuer: A new Enqueuer instance with initialized dependencies
func NewEnqueuer(
	admissionService *admissionmanager.AdmissionManager,
	eventCh chan<- event.GenericEvent,
	client client.Client,
	logger logr.Logger,
) *Enqueuer {
	return &Enqueuer{
		admissionService: admissionService,
		eventCh:          eventCh,
		client:           client,
		logger:           logger,
	}
}

// Start implements the manager.Runnable interface and begins the enqueuer's event loop.
// This method starts listening for admission result changes and triggers workload
// reconciliation when changes occur.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - error: Returns context.Err() if context is cancelled.
func (m *Enqueuer) Start(ctx context.Context) error {
	m.logger.Info("Starting enqueuer")
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Enqueuer stopped due to context cancellation")
			return ctx.Err()
		case admissionResult := <-m.admissionService.AdmissionResultChanged():
			m.logger.Info("enqueuer: Admission result changed", "admissionResult", admissionResult)
			m.checkAndEnqueueEvents(ctx, admissionResult)
		}
	}
}

// checkAndEnqueueEvents processes an admission result change by finding all workloads
// that use the affected admission check and enqueuing reconciliation events for them.
// This ensures that workloads are re-evaluated when their admission check results change.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - admissionResult: The admission result that has changed
func (m *Enqueuer) checkAndEnqueueEvents(ctx context.Context, admissionResult result.AdmissionResult) {
	workloads := &kueue.WorkloadList{}
	if err := m.client.List(
		ctx,
		workloads,
		client.MatchingFields{constant.WorkloadsWithAdmissionCheckKey: admissionResult.CheckName()},
	); err != nil {
		m.logger.Error(err, "Failed to list workloads")
		return
	}

	for _, wl := range workloads.Items {
		// Enqueue generic event to trigger workload reconciliation,
		// this will trigger the workload controller to reconcile the workload
		// and update the AdmissionCheck status
		select {
		case m.eventCh <- event.GenericEvent{Object: &wl}:
			m.logger.Info("Enqueuing event for workload", "workload", wl.Name)
		case <-time.After(15 * time.Second):
			m.logger.Info("Events channel full, skipping enqueue", "workload", wl.Name)
		}
	}
}
