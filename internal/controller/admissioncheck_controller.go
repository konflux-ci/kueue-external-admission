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
	config, err := r.parseACConfig(ctx, ac)
	if err != nil {
		log.Error(err, "failed to parse AdmissionCheck configuration")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to parse AdmissionCheck configuration: "+err.Error())
	}
	if config == nil {
		log.Error(nil, "failed to parse AdmissionCheck configuration")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to parse AdmissionCheck configuration")
	}

	// Create or update AlertManager admitter
	admitter, err := watcher.NewAlertManagerAdmitter(
		config.AlertManager.URL,
		config.AlertFilters.AlertNames,
		log.WithValues("admissionCheck", req.Name),
	)
	if err != nil {
		log.Error(err, "Failed to create AlertManager admitter")
		return r.updateAdmissionCheckStatus(ctx, ac, false, "failed to create AlertManager admitter: "+err.Error())
	}

	// Register the admitter with the shared service (using interface)
	r.admissionService.SetAdmitter(req.Name, admitter)
	log.Info("Created/updated AlertManager admitter for AdmissionCheck", "admissionCheck", req.Name)

	// Update AdmissionCheck status to Active
	return r.updateAdmissionCheckStatus(ctx, ac, true, "AlertManager admission check is active")
}

// parseACConfig parses the AlertManager configuration from AdmissionCheck parameters
func (r *AdmissionCheckReconciler) parseACConfig(ctx context.Context, ac *kueue.AdmissionCheck) (*AlertManagerAdmissionCheckConfig, error) {
	// Check if parameters are specified
	if ac.Spec.Parameters == nil {
		// Use default configuration if no parameters are provided
		return r.getDefaultConfig(), nil
	}

	// Try to get the ExternalAdmissionConfig
	configKey := client.ObjectKey{
		Name:      ac.Spec.Parameters.Name,
		Namespace: ac.Namespace, // Use the same namespace as the AdmissionCheck
	}

	externalConfig := &konfluxciv1alpha1.ExternalAdmissionConfig{}
	if err := r.Get(ctx, configKey, externalConfig); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Config not found, use default configuration
			return r.getDefaultConfig(), nil
		}
		return nil, err
	}

	// Convert ExternalAdmissionConfig to internal format
	return r.convertExternalConfig(externalConfig), nil
}

// convertExternalConfig converts ExternalAdmissionConfig to internal AlertManagerAdmissionCheckConfig
func (r *AdmissionCheckReconciler) convertExternalConfig(external *konfluxciv1alpha1.ExternalAdmissionConfig) *AlertManagerAdmissionCheckConfig {
	// Check if AlertManager provider config exists
	if external.Spec.Provider.AlertManager == nil {
		// No AlertManager config provided, use default
		return r.getDefaultConfig()
	}

	alertMgrConfig := external.Spec.Provider.AlertManager

	// Collect all alert names from all filters
	var allAlertNames []string
	for _, filter := range alertMgrConfig.AlertFilters {
		allAlertNames = append(allAlertNames, filter.AlertNames...)
	}

	config := &AlertManagerAdmissionCheckConfig{
		AlertManager: AlertManagerConfig{
			URL: alertMgrConfig.Connection.URL,
		},
		AlertFilters: AlertFiltersConfig{
			AlertNames: allAlertNames,
		},
		Polling: PollingConfig{
			Interval:         30 * time.Second,
			FailureThreshold: 3,
		},
	}

	// Set timeout if specified
	if alertMgrConfig.Connection.Timeout != nil {
		config.AlertManager.Timeout = alertMgrConfig.Connection.Timeout.Duration
	} else {
		config.AlertManager.Timeout = 10 * time.Second
	}

	// Set polling interval if specified
	if alertMgrConfig.Polling.Interval != nil {
		config.Polling.Interval = alertMgrConfig.Polling.Interval.Duration
	}

	// Set failure threshold if specified
	if alertMgrConfig.Polling.FailureThreshold > 0 {
		config.Polling.FailureThreshold = alertMgrConfig.Polling.FailureThreshold
	}

	return config
}

// getDefaultConfig returns a default configuration when no external config is available
func (r *AdmissionCheckReconciler) getDefaultConfig() *AlertManagerAdmissionCheckConfig {
	return &AlertManagerAdmissionCheckConfig{
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

// findAdmissionChecksForConfig finds AdmissionChecks that should be reconciled
// when an ExternalAdmissionConfig changes
func (r *AdmissionCheckReconciler) findAdmissionChecksForConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*konfluxciv1alpha1.ExternalAdmissionConfig)
	if !ok {
		return nil
	}

	// Find all AdmissionChecks that reference this config
	admissionChecks := &kueue.AdmissionCheckList{}
	if err := r.List(ctx, admissionChecks); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, ac := range admissionChecks.Items {
		// Check if this AdmissionCheck is managed by our controller and references this config
		if ac.Spec.ControllerName == constant.ControllerName &&
			ac.Spec.Parameters != nil &&
			ac.Spec.Parameters.Name == config.Name {

			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      ac.Name,
					Namespace: ac.Namespace,
				},
			})
		}
	}

	return requests
}
