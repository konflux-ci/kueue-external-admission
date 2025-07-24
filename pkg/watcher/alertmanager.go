package watcher

import (
	"context"
	"fmt"
	"net/url"

	"github.com/go-logr/logr"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	alertclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	ShouldAdmit(context.Context) (AdmissionResult, error)
}

// AlertManagerAdmitter implements Admitter using AlertManager API
type AlertManagerAdmitter struct {
	client       *alertclient.AlertmanagerAPI
	config       *konfluxciv1alpha1.AlertManagerProviderConfig
	alertFilters []string
	logger       logr.Logger
}

var _ Admitter = &AlertManagerAdmitter{}

// NewAlertManagerAdmitter creates a new AlertManager client using the official API v2 client
func NewAlertManagerAdmitter(
	config *konfluxciv1alpha1.AlertManagerProviderConfig,
	logger logr.Logger,
) (*AlertManagerAdmitter, error) {
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

	return &AlertManagerAdmitter{
		client:       client,
		config:       config,
		alertFilters: allAlertNames,
		logger:       logger,
	}, nil
}

// ShouldAdmit implements Admitter interface
// Returns an AdmissionResult indicating whether to admit and any firing alerts
func (a *AlertManagerAdmitter) ShouldAdmit(ctx context.Context) (AdmissionResult, error) {
	result := &defaultAdmissionResult{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}

	alerts, err := a.getActiveAlerts(ctx)
	if err != nil {
		a.logger.Error(err, "Failed to get alerts from AlertManager")
		return nil, err
	}

	// If no alert filters are specified, admit by default
	if len(a.alertFilters) == 0 {
		a.logger.Info("No alert filters configured, admitting by default")
		return result, nil
	}

	// Check if any alerts are firing
	firingAlerts := a.findFiringAlerts(alerts)
	if len(firingAlerts) > 0 {
		alertNames := a.getAlertNames(firingAlerts)
		result.addProviderDetails("alertmanager", alertNames)
		result.setAdmissionDenied()
	}

	a.logger.V(1).Info("AlertManager admission check completed",
		"shouldAdmit", result.ShouldAdmit(),
		"firingAlerts", len(firingAlerts),
	)

	return result, nil
}

// getActiveAlerts queries the AlertManager v2 API for active alerts using the official client
func (a *AlertManagerAdmitter) getActiveAlerts(ctx context.Context) ([]*models.GettableAlert, error) {
	params := alert.NewGetAlertsParamsWithContext(ctx).WithDefaults()

	// Query alerts using the official client
	resp, err := a.client.Alert.GetAlerts(params)
	if err != nil {
		return nil, fmt.Errorf("failed to query AlertManager: %w", err)
	}

	return resp.Payload, nil
}

// findFiringAlerts filters alerts to find those that are currently firing and match our filters
func (a *AlertManagerAdmitter) findFiringAlerts(alerts []*models.GettableAlert) []*models.GettableAlert {
	var firingAlerts []*models.GettableAlert

	for _, alertPtr := range alerts {
		if alertPtr == nil || alertPtr.Status == nil {
			continue
		}

		// Skip if not firing
		if *alertPtr.Status.State != "firing" {
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
func (a *AlertManagerAdmitter) matchesFilters(alert *models.GettableAlert) bool {
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
func (a *AlertManagerAdmitter) getAlertNames(alerts []*models.GettableAlert) []string {
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
func (a *AlertManagerAdmitter) GetFiringAlerts(ctx context.Context) ([]string, error) {
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
func (a *AlertManagerAdmitter) getAlertName(alert *models.GettableAlert) string {
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
