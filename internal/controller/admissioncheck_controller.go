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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	"github.com/konflux-ci/kueue-external-admission/pkg/watcher"
	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
)

// NewAdmissionCheckReconciler creates a new AdmissionCheckReconciler
func NewAdmissionCheckReconciler(client client.Client, scheme *runtime.Scheme, admissionService *watcher.AdmissionService) *AdmissionCheckReconciler {
	return &AdmissionCheckReconciler{
		Client:           client,
		Scheme:           scheme,
		admissionService: admissionService,
	}
}

// AdmissionCheckReconciler reconciles a AdmissionCheck object
type AdmissionCheckReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	admissionService *watcher.AdmissionService // Shared service for managing admitters
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

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
	config := r.parseACConfig(ac)
	if config == nil {
		log.Error(nil, "failed to parse AdmissionCheck configuration")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to parse AdmissionCheck configuration")
	}

	// Create or update AlertManager admitter
	admitter := watcher.NewAlertManagerAdmitter(
		config.AlertManager.URL,
		config.AlertFilters.AlertNames,
		log.WithValues("admissionCheck", req.Name),
	)

	// Register the admitter with the shared service (using interface)
	r.admissionService.SetAdmitter(req.Name, admitter)
	log.Info("Created/updated AlertManager admitter for AdmissionCheck", "admissionCheck", req.Name)

	// Update AdmissionCheck status to Active
	return r.updateAdmissionCheckStatus(ctx, ac, true, "AlertManager admission check is active")
}

// parseACConfig parses the AlertManager configuration from AdmissionCheck parameters
func (r *AdmissionCheckReconciler) parseACConfig(_ *kueue.AdmissionCheck) *AlertManagerAdmissionCheckConfig {
	// For now, use a simple hardcoded configuration until we implement proper parameter parsing
	// TODO: Implement proper parameter parsing based on Kueue's parameter structure
	// This would involve reading from ac.Spec.Parameters and parsing the configuration

	// Create a default configuration for demonstration
	config := &AlertManagerAdmissionCheckConfig{
		AlertManager: AlertManagerConfig{
			URL:     "http://alertmanager-operated.monitoring.svc.cluster.local:9093",
			Timeout: 10 * time.Second,
		},
		AlertFilters: AlertFiltersConfig{
			AlertNames: []string{
				"HighCPUUsage",
				"HighMemoryUsage",
				"NodeNotReady",
				"KubernetesPodCrashLooping",
				"DiskSpaceRunningLow",
			},
		},
		Polling: PollingConfig{
			Interval:         30 * time.Second,
			FailureThreshold: 3,
		},
	}

	return config
}

// updateAdmissionCheckStatus updates the status of an AdmissionCheck
func (r *AdmissionCheckReconciler) updateAdmissionCheckStatus(ctx context.Context, ac *kueue.AdmissionCheck, active bool, message string) (ctrl.Result, error) {
	status := metav1.ConditionTrue
	reason := "Active"
	if !active {
		status = metav1.ConditionFalse
		reason = "Inactive"
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
		return ac.Spec.ControllerName == ControllerName
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AdmissionCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueue.AdmissionCheck{}, builder.WithPredicates(ControllerNamePredicate())).
		Named("admissioncheck").
		Complete(r)
}
