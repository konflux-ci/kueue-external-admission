// Package alertmanager provides an admission check implementation that integrates
// with Prometheus AlertManager. It monitors active alerts and makes admission
// decisions based on whether specific alerts are currently firing.
//
// The AlertManager admitter queries the AlertManager API v2 to check for active
// alerts that match configured filters. If any matching alerts are firing,
// workloads are denied admission. If no matching alerts are firing, workloads
// are allowed admission.
package alertmanager

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	alertclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// admitter implements the admission.Admitter interface using AlertManager API v2.
// It monitors active alerts and makes admission decisions based on whether
// configured alert filters are currently firing.
type admitter struct {
	client             *alertclient.AlertmanagerAPI                  // AlertManager API v2 client
	config             *konfluxciv1alpha1.AlertManagerProviderConfig // Configuration for the AlertManager provider
	alertFilters       []string                                      // List of alert names to monitor
	logger             logr.Logger                                   // Logger for structured logging
	admissionCheckName string                                        // Name of the admission check
}

// Compile-time check to ensure admitter implements admission.Admitter interface
var _ admission.Admitter = &admitter{}

// readBearerToken reads a bearer token from a file for authentication.
// The token file should contain a single line with the bearer token.
//
// Parameters:
//   - tokenFile: Path to the file containing the bearer token
//
// Returns:
//   - string: The bearer token read from the file
//   - error: Returns an error if the file cannot be read or is empty
func readBearerToken(tokenFile string) (string, error) {
	// Read token from file
	file, err := os.Open(tokenFile)
	if err != nil {
		return "", fmt.Errorf("failed to open token file %q: %w", tokenFile, err)
	}
	defer func() {
		_ = file.Close() // Ignore close error as the token has already been read successfully
	}()

	tokenBytes, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read token file %q: %w", tokenFile, err)
	}

	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return "", fmt.Errorf("token file %q is empty", tokenFile)
	}

	return token, nil
}

// NewAlertManagerClient creates a new AlertManager API v2 client with optional authentication.
// It configures the HTTP transport and authentication based on the provided parameters.
//
// Parameters:
//   - alertManagerURL: The URL of the AlertManager instance (e.g., "http://alertmanager:9093")
//   - bearerTokenFile: Optional path to a file containing a bearer token for authentication
//
// Returns:
//   - *alertclient.AlertmanagerAPI: Configured AlertManager API v2 client
//   - error: Returns an error if the URL is invalid or authentication setup fails
func NewAlertManagerClient(alertManagerURL string, bearerTokenFile *string) (*alertclient.AlertmanagerAPI, error) {
	// Parse the AlertManager URL
	u, err := url.Parse(alertManagerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid AlertManager URL %q: %w", alertManagerURL, err)
	}

	// Create transport config
	transportConfig := alertclient.DefaultTransportConfig().
		WithHost(u.Host).
		WithBasePath(u.Path).
		WithSchemes([]string{u.Scheme})

	// Create HTTP transport
	transport := httptransport.New(transportConfig.Host, transportConfig.BasePath, transportConfig.Schemes)
	// Note: Timeout is handled by the transport config and runtime

	// Configure authentication if provided
	if bearerTokenFile != nil {
		// Read token from file
		token, err := readBearerToken(*bearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read bearer token from file: %w", err)
		}
		// Set the Authorization header for bearer token authentication
		transport.DefaultAuthentication = httptransport.BearerToken(token)
	}

	// Create the AlertManager API client
	client := alertclient.New(transport, strfmt.Default)

	return client, nil
}

// NewAdmitter creates a new AlertManager admitter instance.
// It initializes the AlertManager API client and processes the configuration
// to extract alert filters for monitoring.
//
// Parameters:
//   - config: AlertManager provider configuration including connection details and alert filters
//   - logger: Logger for structured logging
//   - admissionCheckName: Name of the admission check this admitter will handle
//
// Returns:
//   - admission.Admitter: A new AlertManager admitter instance
//   - error: Returns an error if client creation or configuration processing fails
func NewAdmitter(
	config *konfluxciv1alpha1.AlertManagerProviderConfig,
	logger logr.Logger,
	admissionCheckName string,
) (admission.Admitter, error) {
	// Create the AlertManager API client
	var bearerTokenFile *string
	if config.Connection.BearerToken != nil {
		bearerTokenFile = &config.Connection.BearerToken.TokenFile
	}
	client, err := NewAlertManagerClient(config.Connection.URL, bearerTokenFile)
	if err != nil {
		return nil, err
	}

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
		admissionCheckName: admissionCheckName,
	}, nil
}

// Sync runs the admission check in a loop and sends the results to the channel.
// This method implements the admission.Admitter interface and blocks until the context is cancelled.
// It periodically queries AlertManager for active alerts and sends admission results
// based on whether configured alert filters are firing.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - results: Channel for sending admission results asynchronously
//
// Returns:
//   - error: Returns context.Err() when the context is cancelled
func (a *admitter) Sync(ctx context.Context, results chan<- result.AsyncAdmissionResult) error {
	// Use configured interval or default to 30 seconds
	interval := 30 * time.Second
	if a.config.SyncConfig != nil && a.config.SyncConfig.Interval != nil {
		interval = a.config.SyncConfig.Interval.Duration
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("AlertManager admission check cancelled by context")
			return ctx.Err()
		case <-ticker.C:
			a.logger.Info("Starting sync iteration", "admissionCheck", a.admissionCheckName)
			ret, err := a.shouldAdmit(ctx)
			if err != nil {
				a.logger.Error(err, "Failed to get alerts from AlertManager")
				results <- result.AsyncAdmissionResult{
					AdmissionResult: nil,
					Error:           err,
				}
			} else {
				results <- result.AsyncAdmissionResult{
					AdmissionResult: ret,
					Error:           nil,
				}
			}
		}
	}
}

// shouldAdmit performs a single admission check by querying AlertManager for active alerts.
// It returns an AdmissionResult indicating whether to admit the workload based on
// whether any configured alert filters are currently firing.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - result.AdmissionResult: The admission decision with details about firing alerts
//   - error: Returns an error if the AlertManager query fails
func (a *admitter) shouldAdmit(ctx context.Context) (result.AdmissionResult, error) {
	builder := result.NewAdmissionResultBuilder(a.admissionCheckName)
	builder.SetAdmissionDenied()

	alerts, err := a.getActiveAlerts(ctx)
	if err != nil {
		a.logger.Error(err, "Failed to get alerts from AlertManager")
		return builder.Build(), err
	}

	// If no alert filters are specified, admit by default
	if len(a.alertFilters) == 0 {
		a.logger.Info("No alert filters configured, admitting by default")
		builder.SetAdmissionAllowed()
		return builder.Build(), nil
	}

	// Check if any alerts are firing
	firingAlerts := a.findFiringAlerts(alerts)
	if len(firingAlerts) > 0 {
		alertNames := a.getAlertNames(firingAlerts)
		builder.AddDetails(alertNames...)
		builder.SetAdmissionDenied()
	} else {
		builder.SetAdmissionAllowed()
	}

	result := builder.Build()

	a.logger.Info("AlertManager admission check completed",
		"shouldAdmit", result.ShouldAdmit(),
		"firingAlerts", len(firingAlerts),
	)

	return result, nil
}

// getActiveAlerts queries the AlertManager v2 API for all active alerts.
// It uses the official AlertManager API v2 client to retrieve current alert status.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - []*models.GettableAlert: List of all active alerts from AlertManager
//   - error: Returns an error if the API query fails
func (a *admitter) getActiveAlerts(ctx context.Context) ([]*models.GettableAlert, error) {
	params := alert.NewGetAlertsParamsWithContext(ctx).WithDefaults()

	// Query alerts using the official client
	resp, err := a.client.Alert.GetAlerts(params)
	if err != nil {
		return nil, fmt.Errorf("failed to query AlertManager: %w", err)
	}

	return resp.Payload, nil
}

// findFiringAlerts filters alerts to find those that are currently firing and match configured filters.
// It checks each alert's state and applies the configured alert name filters.
//
// Parameters:
//   - alerts: List of all alerts from AlertManager
//
// Returns:
//   - []*models.GettableAlert: List of alerts that are firing and match the configured filters
func (a *admitter) findFiringAlerts(alerts []*models.GettableAlert) []*models.GettableAlert {
	var firingAlerts []*models.GettableAlert

	for _, alertPtr := range alerts {
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

// matchesFilters checks if an alert matches any of the configured alert name filters.
// Currently supports exact matching on the "alertname" label.
//
// Parameters:
//   - alert: The alert to check against configured filters
//
// Returns:
//   - bool: True if the alert matches any configured filter, false otherwise
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

// getAlertNames extracts alert names from a list of alerts for logging purposes.
// It extracts the "alertname" label from each alert.
//
// Parameters:
//   - alerts: List of alerts to extract names from
//
// Returns:
//   - []string: List of alert names extracted from the alerts
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

// GetFiringAlerts returns the names of currently firing alerts that match configured filters.
// This is a utility method that can be used to query the current state of firing alerts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - []string: List of names of currently firing alerts that match the filters
//   - error: Returns an error if the AlertManager query fails
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

// getAlertName extracts a meaningful name for an alert from its labels.
// It tries multiple strategies to find a suitable name: alertname label,
// job:instance combination, or any other meaningful label combination.
//
// Parameters:
//   - alert: The alert to extract a name from
//
// Returns:
//   - string: A meaningful name for the alert, or "unknown-alert" if no suitable name is found
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

// Equal reports whether this AlertManager admitter is functionally equivalent to another admitter.
// It compares the relevant configuration fields that determine the admitter's behavior,
// but ignores internal state like HTTP clients, loggers, etc. This is used by the
// AdmitterManager to avoid unnecessary admitter replacements.
//
// Parameters:
//   - other: Another admitter to compare against
//
// Returns:
//   - bool: True if the admitters are functionally equivalent, false otherwise
func (a *admitter) Equal(other admission.Admitter) bool {
	// Type assertion to check if the other admitter is also an AlertManager admitter
	otherAlertManager, ok := other.(*admitter)
	if !ok {
		return false
	}

	// Compare admission check names
	if a.admissionCheckName != otherAlertManager.admissionCheckName {
		return false
	}

	// Compare alert filters (converted from config)
	if len(a.alertFilters) != len(otherAlertManager.alertFilters) {
		return false
	}
	for i, filter := range a.alertFilters {
		if filter != otherAlertManager.alertFilters[i] {
			return false
		}
	}

	// Compare the relevant configuration fields using reflect.DeepEqual
	// This is safe because we're comparing the configuration structs, not the admitter instances
	return reflect.DeepEqual(a.config, otherAlertManager.config)
}
