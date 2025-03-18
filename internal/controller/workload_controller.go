/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"slices"

	"github.com/konflux-ci/kueue-external-admission/pkg/watcher"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/util/admissioncheck"
	"sigs.k8s.io/kueue/pkg/workload"
)

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client   client.Client
	Scheme   *runtime.Scheme
	admitter watcher.Admitter
}

func NewWorkloadController(client client.Client, schema *runtime.Scheme, admitter watcher.Admitter) *WorkloadReconciler {
	return &WorkloadReconciler{
		client,
		schema,
		admitter,
	}
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Workload object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/reconcile
func (w *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	wl := &kueue.Workload{}

	err := w.client.Get(ctx, req.NamespacedName, wl)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconcile", "Workload", wl.Name)

	if !workload.HasQuotaReservation(wl) || workload.IsFinished(wl) || workload.IsEvicted(wl) || workload.IsAdmitted(wl) {
		return reconcile.Result{}, nil
	}

	// Get the checks which are relevant to our controller. AC parameters are not supported
	relevantChecks, err := admissioncheck.FilterForController(ctx, w.client, wl.Status.AdmissionChecks, ControllerName)
	if err != nil {
		return reconcile.Result{}, err
	}

	if len(relevantChecks) == 0 {
		log.Info("Didn't find relevant checks")
		return reconcile.Result{}, nil
	}

	// todo: move into a function
	shouldAdmit, err := w.admitter.ShouldAdmit(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	wlPatch := workload.BaseSSAWorkload(wl)
	for _, check := range relevantChecks {
		state := kueue.CheckStatePending
		message := "denying workload"

		if shouldAdmit {
			state = kueue.CheckStateReady
			message = "approving workload"
		}

		newCheck := kueue.AdmissionCheckState{
			Name:    check,
			State:   state,
			Message: message,
		}

		workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, newCheck)
	}

	// make the update only if the workload was changed?
	err = w.client.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(ControllerName), client.ForceOwnership)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (w *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager, eventsChan <-chan event.GenericEvent) error {
	return ctrl.NewControllerManagedBy(mgr).
		// todo: reconcile workloads when a new admission check is added to the cluster
		For(&kueue.Workload{}).
		Named("workload").
		WatchesRawSource(
			source.Channel(
				eventsChan,
				&handler.EnqueueRequestForObject{},
			),
		).
		Complete(w)
}

type WorkloadLister struct {
	client client.Client
}

var _ watcher.Lister = &WorkloadLister{}

func NewWorkloadLister(client client.Client) *WorkloadLister {
	return &WorkloadLister{client: client}
}

// List implements watcher.Lister.
// Subtle: this method shadows the method (Client).List of WorkloadLister.Client.
func (w *WorkloadLister) List(ctx context.Context) ([]client.Object, error) {
	ww := &kueue.WorkloadList{}
	if err := w.client.List(ctx, ww, &client.ListOptions{
		// For performance reasons we'll deepcopy after filtering the list
		// IMPORTANT: DO NOT make any change to the listed workspaces
		UnsafeDisableDeepCopy: ptr.To(true),
	}); err != nil {
		return nil, err
	}

	// let's prepare for the worst case, we'll clip the slice before returning it
	workloads := make([]client.Object, 0, len(ww.Items))
	// build the filtered list of workspaces
	for _, iw := range ww.Items {
		if w.isWorkloadAdmittedAndNotFinished(&iw) {
			// IMPORTANT: as we didn't DeepCopy before, we NEED to DeepCopy now
			workloads = append(workloads, iw.DeepCopy())
		}
	}

	// reduce the capacity of the list before returning it
	return slices.Clip(workloads), nil
}

// isWorkloadAdmittedAndNotFinished returns true if the workload has been admitted
// and hasn't still finished.
func (w *WorkloadLister) isWorkloadAdmittedAndNotFinished(workload *kueue.Workload) bool {
	admitted := len(workload.Status.AdmissionChecks) == 0
	finished := meta.IsStatusConditionTrue(workload.Status.Conditions, kueue.WorkloadFinished)

	return admitted && !finished
}
