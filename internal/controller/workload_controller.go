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
	"fmt"
	"slices"
	"strings"

	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/enqueue"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/util/admissioncheck"
	"sigs.k8s.io/kueue/pkg/workload"

	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
)

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client   client.Client
	Scheme   *runtime.Scheme
	admitter admission.MultiCheckAdmitter
	clock    clock.Clock
}

func NewWorkloadController(
	client client.Client,
	schema *runtime.Scheme,
	admitter admission.MultiCheckAdmitter,
	clock clock.Clock,
) *WorkloadReconciler {
	return &WorkloadReconciler{
		client,
		schema,
		admitter,
		clock,
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

	// wl.Status.AdmissionChecks contains only the admission checks configured
	// for the ClusterQueue that this workload belongs to. This is populated by Kueue
	// based on the ClusterQueue's admissionChecks configuration.
	log.Info("Processing admission checks for workload",
		"workload", wl.Name,
		"totalAdmissionChecks", len(wl.Status.AdmissionChecks))

	// Filter to get only the admission checks managed by our controller
	// from the ones configured for this workload's ClusterQueue
	relevantChecks, err := admissioncheck.FilterForController(ctx, w.client, wl.Status.AdmissionChecks, constant.ControllerName)
	if err != nil {
		return reconcile.Result{}, err
	}

	if len(relevantChecks) == 0 {
		log.Info("No admission checks managed by our controller found for this workload",
			"workload", wl.Name,
			"controller", constant.ControllerName)
		return reconcile.Result{}, nil
	}

	log.Info("Found admission checks managed by our controller",
		"workload", wl.Name,
		"controller", constant.ControllerName,
		"relevantChecks", relevantChecks)

	// Use only the admission checks configured for this workload's ClusterQueue
	// that are managed by our controller
	admissionCheckNames := relevantChecks

	// Check admission using the shared service - this will only check the
	// admitters for the specific admission checks configured
	// for this workload's ClusterQueue
	admissionResult, err := w.admitter.ShouldAdmitWorkload(ctx, admissionCheckNames)
	if err != nil {
		log.Error(err, "Error checking admission for workload", "workload", wl.Name)
		return reconcile.Result{}, err
	}

	wlPatch := workload.BaseSSAWorkload(wl)
	for _, check := range relevantChecks {
		state := kueue.CheckStatePending
		message := "denying workload"

		if admissionResult.ShouldAdmit() {
			state = kueue.CheckStateReady
			message = "approving workload"
		} else {
			// Include provider details in the denial message
			providerDetails := admissionResult.GetProviderDetails()
			if details, exists := providerDetails[check]; exists && len(details) > 0 {
				if len(details) == 1 {
					message = fmt.Sprintf("denying workload due to: %s", details[0])
				} else {
					message = fmt.Sprintf("denying workload due to: %s", strings.Join(details, ", "))
				}
			}
		}

		newCheck := kueue.AdmissionCheckState{
			Name:    check,
			State:   state,
			Message: message,
		}

		workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, newCheck, w.clock)
	}

	// TODO: make the update only if the workload was changed?
	err = w.client.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(constant.ControllerName), client.ForceOwnership)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (w *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager, eventsChan <-chan event.GenericEvent) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueue.Workload{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(
			source.Channel(
				eventsChan,
				&handler.EnqueueRequestForObject{},
			),
		).
		Named("workload").
		Complete(w)
}

type WorkloadLister struct {
	client client.Client
}

var _ enqueue.Lister = &WorkloadLister{}

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

// isWorkloadAdmittedAndNotFinished returns true if the workload requires admission checks
// and hasn't still finished.
func (w *WorkloadLister) isWorkloadAdmittedAndNotFinished(workload *kueue.Workload) bool {
	requiresAdmissionCheck := len(workload.Status.AdmissionChecks) > 0
	finished := meta.IsStatusConditionTrue(workload.Status.Conditions, kueue.WorkloadFinished)

	return requiresAdmissionCheck && !finished
}
