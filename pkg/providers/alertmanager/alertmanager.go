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

package alertmanager

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-logr/logr"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	alertclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/watcher"
)

func init() {
	// Register this provider's factory with the watcher package
	watcher.RegisterProviderFactory("alertmanager",
		func(
			config *konfluxciv1alpha1.ExternalAdmissionConfig,
			logger logr.Logger,
			admissionCheckName string,
		) (watcher.Admitter, error) {
			if config.Spec.Provider.AlertManager == nil {
				return nil, fmt.Errorf("AlertManager provider config is nil")
			}
			return NewAdmitter(config.Spec.Provider.AlertManager, logger, admissionCheckName)
		})
}

// admitter implements the watcher.admitter interface using AlertManager API
type admitter struct {
	client             *alertclient.AlertmanagerAPI
	config             *konfluxciv1alpha1.AlertManagerProviderConfig
	alertFilters       []string
	logger             logr.Logger
	cache              *watcher.Cache[watcher.AdmissionResult]
	admissionCheckName string
}

var _ watcher.Admitter = &admitter{}

// NewAdmitter creates a new AlertManager client using the official API v2 client
func NewAdmitter(
	config *konfluxciv1alpha1.AlertManagerProviderConfig,
	logger logr.Logger,
	admissionCheckName string,
) (watcher.Admitter, error) {
	// Parse the AlertManager URL
	u, err := url.Parse(config.Connection.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid AlertManager URL %q: %w", config.Connection.URL, err)
	}

	// Create transport config
	transportConfig := alertclient.DefaultTransportConfig().
		WithHost(u.Host).
		WithBasePath(u.Path).
		WithSchemes([]string{u.Scheme})

	// Create HTTP transport
	transport := httptransport.New(transportConfig.Host, transportConfig.BasePath, transportConfig.Schemes)
	// Note: Timeout is handled by the transport config and runtime

	// Create the AlertManager API client
	client := alertclient.New(transport, strfmt.Default)

	// Collect all alert names from all filters
	var allAlertNames []string
	for _, filter := range config.AlertFilters {
		allAlertNames = append(allAlertNames, filter.AlertNames...)
	}

	return &admitter{
		client:             client,
		config:             config,
		alertFilters:       allAlertNames,
		logger:             logger,
		cache:              watcher.NewCache[watcher.AdmissionResult](config.CheckTTL.Duration),
		admissionCheckName: admissionCheckName,
	}, nil
}

// Sync runs the admission check in a loop and sends the results to the channel
// the call doesn't block
func (a *admitter) Sync(ctx context.Context, results chan<- watcher.AsyncAdmissionResult) error {
	go func() {
		ticker := time.NewTicker(a.config.CheckTTL.Duration)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				a.logger.Info("AlertManager admission check cancelled by context")
				return
			case <-ticker.C:
				a.logger.Info("Running AlertManager admission check")
				result, err := a.shouldAdmit(ctx)
				if err != nil {
					a.logger.Error(err, "Failed to get alerts from AlertManager")
					results <- watcher.AsyncAdmissionResult{
						AdmissionResult: nil,
						Error:           err,
					}
				} else {
					results <- watcher.AsyncAdmissionResult{
						AdmissionResult: result,
						Error:           nil,
					}
				}
			}
		}
	}()
	return nil
}

func (a *admitter) ShouldAdmit(ctx context.Context) (watcher.AdmissionResult, error) {
	return a.cache.GetOrUpdate(ctx, a.shouldAdmit)
}

// ShouldAdmit implements watcher.Admitter interface
// Returns an AdmissionResult indicating whether to admit and any firing alerts
func (a *admitter) shouldAdmit(ctx context.Context) (watcher.AdmissionResult, error) {
	builder := watcher.NewAdmissionResult()
	builder.AddProviderDetails(a.admissionCheckName, []string{})

	alerts, err := a.getActiveAlerts(ctx)
	if err != nil {
		a.logger.Error(err, "Failed to get alerts from AlertManager")
		return nil, err
	}

	// If no alert filters are specified, admit by default
	if len(a.alertFilters) == 0 {
		a.logger.Info("No alert filters configured, admitting by default")
		return builder.Build(), nil
	}

	// Check if any alerts are firing
	firingAlerts := a.findFiringAlerts(alerts)
	if len(firingAlerts) > 0 {
		alertNames := a.getAlertNames(firingAlerts)
		builder.AddProviderDetails(a.admissionCheckName, alertNames)
		builder.SetAdmissionDenied()
	}

	result := builder.Build()

	a.logger.V(1).Info("AlertManager admission check completed",
		"shouldAdmit", result.ShouldAdmit(),
		"firingAlerts", len(firingAlerts),
	)

	return result, nil
}

// getActiveAlerts queries the AlertManager v2 API for active alerts using the official client
func (a *admitter) getActiveAlerts(ctx context.Context) ([]*models.GettableAlert, error) {
	params := alert.NewGetAlertsParamsWithContext(ctx).WithDefaults()

	// Query alerts using the official client
	resp, err := a.client.Alert.GetAlerts(params)
	if err != nil {
		return nil, fmt.Errorf("failed to query AlertManager: %w", err)
	}

	return resp.Payload, nil
}

// findFiringAlerts filters alerts to find those that are currently firing and match our filters
func (a *admitter) findFiringAlerts(alerts []*models.GettableAlert) []*models.GettableAlert {
	var firingAlerts []*models.GettableAlert

	for _, alertPtr := range alerts {
		a.logger.Info("Alert", "alert", alertPtr)
		if alertPtr == nil || alertPtr.Status == nil {
			continue
		}

		// Skip if not firing
		if *alertPtr.Status.State != models.AlertStatusStateActive {
			continue
		}

		// Check if this alert matches any of our filters
		if a.matchesFilters(alertPtr) {
			firingAlerts = append(firingAlerts, alertPtr)
		}
	}

	return firingAlerts
}

// matchesFilters checks if an alert matches any of the configured filters
func (a *admitter) matchesFilters(alert *models.GettableAlert) bool {
	if alert.Labels == nil {
		return false
	}

	for _, filter := range a.alertFilters {
		// Check if filter matches alert name
		if alertName, exists := alert.Labels["alertname"]; exists && alertName == filter {
			return true
		}

		// TODO: Add support for more complex label matching
		// For now, we only support exact alertname matching
	}

	return false
}

// getAlertNames extracts alert names from alerts for logging
func (a *admitter) getAlertNames(alerts []*models.GettableAlert) []string {
	var names []string
	for _, alert := range alerts {
		if alert.Labels != nil {
			if name, ok := alert.Labels["alertname"]; ok {
				names = append(names, name)
			}
		}
	}
	return names
}

// GetFiringAlerts returns the names of currently firing alerts that match our filters
func (a *admitter) GetFiringAlerts(ctx context.Context) ([]string, error) {
	alerts, err := a.getActiveAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query alerts: %w", err)
	}

	var firingAlerts []string
	for _, alert := range alerts {
		if alert != nil && alert.Status != nil &&
			*alert.Status.State == "firing" && a.matchesFilters(alert) {
			alertName := a.getAlertName(alert)
			firingAlerts = append(firingAlerts, alertName)
		}
	}

	return firingAlerts, nil
}

// getAlertName extracts a meaningful name for the alert
func (a *admitter) getAlertName(alert *models.GettableAlert) string {
	if alert.Labels == nil {
		return "unknown-alert"
	}

	// Try to get the alertname label first
	if alertName, exists := alert.Labels["alertname"]; exists {
		return alertName
	}

	// Fall back to constructing a name from available labels
	if instance, exists := alert.Labels["instance"]; exists {
		if job, exists := alert.Labels["job"]; exists {
			return fmt.Sprintf("%s:%s", job, instance)
		}
		return instance
	}

	// Last resort - use any available label
	for key, value := range alert.Labels {
		if key != "severity" && key != "team" {
			return fmt.Sprintf("%s:%s", key, value)
		}
	}

	return "unknown-alert"
}
