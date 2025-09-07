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

// Enqueuer periodically checks alert states and emits events when changes occur
type Enqueuer struct {
	admissionService *admissionmanager.AdmissionManager
	eventCh          chan<- event.GenericEvent
	client           client.Client
	period           time.Duration
	logger           logr.Logger
}

var _ manager.Runnable = &Enqueuer{}

// NewEnqueuer creates a new alert enqueuer
func NewEnqueuer(
	admissionService *admissionmanager.AdmissionManager,
	eventCh chan<- event.GenericEvent,
	client client.Client,
	period time.Duration,
	logger logr.Logger,
) *Enqueuer {
	return &Enqueuer{
		admissionService: admissionService,
		eventCh:          eventCh,
		client:           client,
		period:           period,
		logger:           logger,
	}
}

// Start implements manager.Runnable interface
func (m *Enqueuer) Start(ctx context.Context) error {
	m.logger.Info("Starting enqueuer")
	m.enqueuerLoop(ctx)
	return nil
}

// enqueuerLoop runs the enqueuer loop
func (m *Enqueuer) enqueuerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Enqueuer stopped due to context cancellation")
			return
		case admissionResult := <-m.admissionService.AdmissionResultChanged():
			m.logger.Info("enqueuer: Admission result changed", "admissionResult", admissionResult)
			m.checkAndEnqueueEvents(ctx, admissionResult)
		}
	}
}

// checkAndEnqueueEvents checks current alert states and emits events for changed workloads
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
