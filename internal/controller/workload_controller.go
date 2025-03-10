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

	"github.com/konflux-ci/kueue-external-admission/pkg/watcher"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/workload"
)

const CheckName = "konflux-ci.dev/kueue-external-admission"
const IndexByStateKey = "status.admissionChecks." + CheckName + ".state"

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client.Client
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

// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=workloads,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=workloads/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=workloads/finalizers,verbs=update

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

	err := w.Get(ctx, req.NamespacedName, wl)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconcile", "Workload", wl.Name)

	if !workload.HasQuotaReservation(wl) || workload.IsFinished(wl) || workload.IsEvicted(wl) || workload.IsAdmitted(wl) {
		return reconcile.Result{}, nil
	}

	// get the lists of relevant checks?

	state := kueue.CheckStatePending
	message := "denying workload"
	shouldAdmit, err := w.admitter.ShouldAdmit()
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldAdmit {
		state = kueue.CheckStateReady
		message = "approving workload"
	}

	newCheck := kueue.AdmissionCheckState{
		Name:    CheckName,
		State:   state,
		Message: message,
	}
	wlPatch := workload.BaseSSAWorkload(wl)
	workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, newCheck)
	// todo: move the field owner to its own var and add a domain name to it
	err = w.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner("am-admission"), client.ForceOwnership)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (w *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager, eventsChan <-chan event.GenericEvent) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
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
func (w *WorkloadLister) List() ([]client.Object, error) {
	wl := &kueue.WorkloadList{}
	// todo: query only queued workloads using an index
	err := w.client.List(context.TODO(), wl, client.MatchingFields{IndexByStateKey: string(kueue.CheckStatePending)})
	if err != nil {
		return nil, err
	}
	objects := make([]client.Object, len(wl.Items))
	for i := range wl.Items {
		objects[i] = &wl.Items[i]
	}

	return objects, nil
}

// index pending workloads which has need our admission check
func SetupIndex(ctx context.Context, indexer client.FieldIndexer) error {
	return indexer.IndexField(ctx, &kueue.Workload{}, IndexByStateKey, func(obj client.Object) []string {
		wl, isWl := obj.(*kueue.Workload)

		if !isWl || len(wl.Status.AdmissionChecks) == 0 {
			return nil
		}

		cs := workload.FindAdmissionCheck(wl.Status.AdmissionChecks, CheckName)
		if cs == nil {
			return nil
		}

		return []string{string(cs.State)}

	})
}
