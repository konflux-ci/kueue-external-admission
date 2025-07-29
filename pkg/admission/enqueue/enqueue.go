package enqueue

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

	admissionmanager "github.com/konflux-ci/kueue-external-admission/pkg/admission/manager"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
)

// Lister provides a way to list objects that need admission checking
type Lister interface {
	List(context.Context) ([]client.Object, error)
}

// Enqueuer periodically checks alert states and emits events when changes occur
type Enqueuer struct {
	admissionService *admissionmanager.AdmissionManager
	lister           Lister
	eventCh          chan<- event.GenericEvent
	client           client.Client
	period           time.Duration
	logger           logr.Logger
}

var _ manager.Runnable = &Enqueuer{}

// NewEnqueuer creates a new alert enqueuer
func NewEnqueuer(
	admissionService *admissionmanager.AdmissionManager,
	lister Lister,
	eventCh chan<- event.GenericEvent,
	client client.Client,
	period time.Duration,
	logger logr.Logger,
) *Enqueuer {
	return &Enqueuer{
		admissionService: admissionService,
		lister:           lister,
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
		// TODO: periodically enqueue all workloads that didn't start,
		// has quota reservation and is not admitted yet and that have admission checks configured relevant to us
	}
}

// checkAndEnqueueEvents checks current alert states and emits events for changed workloads
func (m *Enqueuer) checkAndEnqueueEvents(ctx context.Context, admissionResult result.AdmissionResult) {
	// Get all workloads that need admission checks
	workloads, err := m.lister.List(ctx)
	if err != nil {
		m.logger.Error(err, "Failed to list workloads")
		return
	}
	// TODO: we should iterate over all the admission checks the admission service has
	// and get the workloads for each one of them.
	// UPDATE: since we now know which admission check changed,
	// we can emit events for the workloads that are affected by that change.
	filteredWorkloads := make(map[*kueue.Workload]bool)
	for _, obj := range workloads {
		wl, ok := obj.(*kueue.Workload)
		if !ok {
			continue
		}

		if workload.IsAdmitted(wl) {
			continue
		}

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

		// TODO: this is a hack to get the workloads that are affected by the admission check that changed.
		// we should use an index instead.
		for _, check := range wl.Status.AdmissionChecks {
			if admissionResult.CheckName() == check.Name {
				filteredWorkloads[wl] = true
			}
		}
	}

	for wl := range filteredWorkloads {
		// Enqueue generic event to trigger workload reconciliation
		select {
		case m.eventCh <- event.GenericEvent{Object: wl}:
			m.logger.Info("Enqueuing event for workload", "workload", wl.Name)
		case <-time.After(15 * time.Second):
			m.logger.Info("Events channel full, skipping enqueue", "workload", wl.Name)
		}
	}
}
