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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

const (
	ControllerName = "konflux-ci.dev/alert-manager-admission"
)

// AdmissionCheckReconciler reconciles a AdmissionCheck object
type AdmissionCheckReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=admissionchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=admissionchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io.konflux-ci.dev,resources=admissionchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AdmissionCheck object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/reconcile
func (a *AdmissionCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling", "object", req.NamespacedName)

	ac := &kueue.AdmissionCheck{}
	if err := a.Get(ctx, req.NamespacedName, ac); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	currentCondition := ptr.Deref(apimeta.FindStatusCondition(ac.Status.Conditions, kueue.AdmissionCheckActive), metav1.Condition{})
	newCondition := metav1.Condition{
		Type:               kueue.AdmissionCheckActive,
		Status:             metav1.ConditionTrue,
		Reason:             "Active",
		Message:            "The admission check is active",
		ObservedGeneration: ac.Generation,
	}

	if currentCondition.Status != newCondition.Status {
		log.Info("Updating the status condition for", "object", ac.Name)
		apimeta.SetStatusCondition(&ac.Status.Conditions, newCondition)
		return reconcile.Result{}, a.Status().Update(ctx, ac)
	}

	return ctrl.Result{}, nil
}

func ControllerNamePredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		ac, ok := object.(*kueue.AdmissionCheck)
		if !ok {
			return false
		}

		return ac.Spec.ControllerName == ControllerName
	})
}

// SetupWithManager sets up the controller with the Manager.
func (a *AdmissionCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&kueue.AdmissionCheck{}, builder.WithPredicates(ControllerNamePredicate())).
		Named("admissioncheck").
		Complete(a)
}
