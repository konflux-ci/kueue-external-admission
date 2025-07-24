/*
Copyright 2024.

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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
	"github.com/konflux-ci/kueue-external-admission/pkg/watcher"
	acutil "sigs.k8s.io/kueue/pkg/util/admissioncheck"
)

const admissionCheckConfigNameKey = "spec.parameters.name"

// NewAdmissionCheckReconciler creates a new AdmissionCheckReconciler
func NewAdmissionCheckReconciler(client client.Client, scheme *runtime.Scheme, admissionService *watcher.AdmissionService) (*AdmissionCheckReconciler, error) {
	acHelper, err := acutil.NewConfigHelper[*konfluxciv1alpha1.ExternalAdmissionConfig](client)
	if err != nil {
		return nil, err
	}
	return &AdmissionCheckReconciler{
		Client:           client,
		Scheme:           scheme,
		admissionService: admissionService,
		acHelper:         acHelper,
		admitterFactory:  watcher.NewAdmitter,
	}, nil
}

// AdmissionCheckReconciler reconciles a AdmissionCheck object
type AdmissionCheckReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	admissionService *watcher.AdmissionService // Shared service for managing admitters
	acHelper         *acutil.ConfigHelper[*konfluxciv1alpha1.ExternalAdmissionConfig, konfluxciv1alpha1.ExternalAdmissionConfig]
	admitterFactory  watcher.AdmitterFactory
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups=konflux-ci.dev,resources=externaladmissionconfigs,verbs=get;list;watch

// Reconcile is part of the main reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AdmissionCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling", "object", req.NamespacedName)

	// Fetch the AdmissionCheck instance
	ac := &kueue.AdmissionCheck{}
	if err := r.Get(ctx, req.NamespacedName, ac); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// AdmissionCheck was deleted, clean up admitter
			r.admissionService.RemoveAdmitter(req.Name)
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Only handle our controller
	if ac.Spec.ControllerName != constant.ControllerName {
		return ctrl.Result{}, nil
	}

	// Parse AlertManager configuration from parameters
	externalConfig, err := r.acHelper.ConfigFromRef(ctx, ac.Spec.Parameters)
	if err != nil {
		log.Error(err, "failed to get ExternalAdmissionConfig")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to get ExternalAdmissionConfig: "+err.Error())
	}

	// Create or update Admitter using factory function
	admitter, err := r.admitterFactory(externalConfig, log.WithValues("admissionCheck", req.Name))
	if err != nil {
		log.Error(err, "Failed to create admitter")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to create admitter: "+err.Error())
	}

	// Register the admitter with the shared service (using interface)
	r.admissionService.SetAdmitter(req.Name, admitter)
	log.Info("Created/updated admitter for AdmissionCheck", "admissionCheck", req.Name)

	// Update AdmissionCheck status to Active
	return r.updateAdmissionCheckStatus(ctx, ac, true, "External admission check is active")
}

// updateAdmissionCheckStatus updates the status of an AdmissionCheck
func (r *AdmissionCheckReconciler) updateAdmissionCheckStatus(ctx context.Context, ac *kueue.AdmissionCheck, active bool, message string) (ctrl.Result, error) {
	status := metav1.ConditionTrue
	reason := kueue.AdmissionCheckActive
	if !active {
		status = metav1.ConditionFalse
	}

	currentCondition := ptr.Deref(apimeta.FindStatusCondition(ac.Status.Conditions, kueue.AdmissionCheckActive), metav1.Condition{})
	newCondition := metav1.Condition{
		Type:               kueue.AdmissionCheckActive,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: ac.Generation,
	}

	if currentCondition.Status != newCondition.Status {
		apimeta.SetStatusCondition(&ac.Status.Conditions, newCondition)
		err := r.Status().Update(ctx, ac)
		if err != nil {
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
	}

	return ctrl.Result{}, nil
}

// ControllerNamePredicate returns a predicate that filters for admission checks
// with the correct controller name
func ControllerNamePredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		ac, ok := obj.(*kueue.AdmissionCheck)
		if !ok {
			return false
		}
		return ac.Spec.ControllerName == constant.ControllerName
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AdmissionCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueue.AdmissionCheck{}, builder.WithPredicates(ControllerNamePredicate())).
		// Watch ExternalAdmissionConfig changes and reconcile related AdmissionChecks
		Watches(
			&konfluxciv1alpha1.ExternalAdmissionConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findAdmissionChecksForConfig),
		).
		Named("admissioncheck").
		Complete(r)
}

// SetupIndexer sets up an indexer for AdmissionChecks that reference an ExternalAdmissionConfig
func SetupIndexer(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	return fieldIndexer.IndexField(
		ctx,
		&kueue.AdmissionCheck{},
		admissionCheckConfigNameKey,
		acutil.IndexerByConfigFunction(
			constant.ControllerName,
			konfluxciv1alpha1.GroupVersion.WithKind("ExternalAdmissionConfig"),
		),
	)
}

// findAdmissionChecksForConfig finds AdmissionChecks that should be reconciled
// when an ExternalAdmissionConfig changes
func (r *AdmissionCheckReconciler) findAdmissionChecksForConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*konfluxciv1alpha1.ExternalAdmissionConfig)
	if !ok {
		return nil
	}

	// Find all AdmissionChecks that reference this config
	admissionChecks := &kueue.AdmissionCheckList{}
	if err := r.List(ctx, admissionChecks, client.MatchingFields{admissionCheckConfigNameKey: config.Name}); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(admissionChecks.Items))
	for _, ac := range admissionChecks.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{
				Name:      ac.Name,
				Namespace: ac.Namespace,
			},
		})
	}

	return requests
}
