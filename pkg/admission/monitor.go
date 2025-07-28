package admission

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

	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
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
	m.logger.Info("Starting monitor")
	m.monitorLoop(ctx)
	return nil
}

// monitorLoop runs the monitoring loop
func (m *Monitor) monitorLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Monitor stopped due to context cancellation")
			return
		case admissionResult := <-m.admissionService.AdmissionResultChanged():
			m.logger.Info("monitor: Admission result changed", "admissionResult", admissionResult)
			m.checkAndEmitEvents(ctx, admissionResult)
		}
	}
}

// checkAndEmitEvents checks current alert states and emits events for changed workloads
func (m *Monitor) checkAndEmitEvents(ctx context.Context, admissionResult result.AdmissionResult) {
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
		// Emit generic event to trigger workload reconciliation
		select {
		case m.admissionService.eventsChannel <- event.GenericEvent{Object: wl}:
			m.logger.Info("Emitting event for workload", "workload", wl.Name)
		default:
			m.logger.Info("Events channel full, skipping event", "workload", wl.Name)
		}
	}
}
