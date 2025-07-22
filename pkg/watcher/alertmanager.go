package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-logr/logr"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	ShouldAdmit(context.Context) AdmissionResult
}

// AlertManagerAdmitter queries AlertManager v2 API to check for active alerts
type AlertManagerAdmitter struct {
	client          *http.Client
	alertManagerURL string
	alertFilters    []string // Alert names or label filters to check for
	logger          logr.Logger
}

// AlertManagerAlert represents an alert from AlertManager API v2
type AlertManagerAlert struct {
	Status       string            `json:"status"` // "firing" or "resolved"
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// AlertManagerResponse represents the response from AlertManager API v2
type AlertManagerResponse struct {
	Status string              `json:"status"`
	Data   []AlertManagerAlert `json:"data"`
}

var _ Admitter = &AlertManagerAdmitter{}

// NewAlertManagerAdmitter creates a new AlertManager client
func NewAlertManagerAdmitter(
	alertManagerURL string,
	alertFilters []string,
	logger logr.Logger,
) *AlertManagerAdmitter {
	return &AlertManagerAdmitter{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		alertManagerURL: alertManagerURL,
		alertFilters:    alertFilters,
		logger:          logger,
	}
}

// ShouldAdmit implements Admitter interface
// Returns an AdmissionResult indicating whether to admit and any firing alerts
func (a *AlertManagerAdmitter) ShouldAdmit(ctx context.Context) AdmissionResult {
	result := &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
		errors:       make(map[string]error),
	}

	alerts, err := a.getActiveAlerts(ctx)
	if err != nil {
		a.logger.Error(err, "Failed to get alerts from AlertManager")
		result.addError("alertmanager", err)
		result.setAdmissionDenied()
		return result
	}

	// If no alert filters are specified, admit by default
	if len(a.alertFilters) == 0 {
		a.logger.Info("No alert filters configured, admitting by default")
		return result // Already admits by default
	}

	// Check if any of the filtered alerts are firing
	firingAlerts := a.findFiringAlerts(alerts)
	if len(firingAlerts) > 0 {
		alertNames := a.getAlertNames(firingAlerts)
		result.addFiringAlerts("alertmanager", alertNames)
		result.setAdmissionDenied()

		a.logger.Info("Found firing alerts, denying admission",
			"firingAlerts", len(firingAlerts),
			"alertNames", alertNames)
	} else {
		a.logger.Info("No matching alerts are firing, allowing admission",
			"totalAlerts", len(alerts))
	}

	return result
}

// getActiveAlerts queries the AlertManager v2 API for active alerts
func (a *AlertManagerAdmitter) getActiveAlerts(ctx context.Context) ([]AlertManagerAlert, error) {
	// Build the API URL
	apiURL := fmt.Sprintf("%s/api/v2/alerts", a.alertManagerURL)

	// Add query parameters if needed (e.g., for filtering)
	u, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AlertManager URL: %w", err)
	}

	// Create the request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Make the request
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query AlertManager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("AlertManager returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var alertResponse AlertManagerResponse
	if err := json.NewDecoder(resp.Body).Decode(&alertResponse); err != nil {
		return nil, fmt.Errorf("failed to decode AlertManager response: %w", err)
	}

	if alertResponse.Status != "success" {
		return nil, fmt.Errorf("AlertManager API returned error status: %s", alertResponse.Status)
	}

	return alertResponse.Data, nil
}

// findFiringAlerts filters alerts to find those that are currently firing and match our filters
func (a *AlertManagerAdmitter) findFiringAlerts(alerts []AlertManagerAlert) []AlertManagerAlert {
	var firingAlerts []AlertManagerAlert

	for _, alert := range alerts {
		// Skip if not firing
		if alert.Status != "firing" {
			continue
		}

		// Check if this alert matches any of our filters
		if a.matchesFilters(alert) {
			firingAlerts = append(firingAlerts, alert)
		}
	}

	return firingAlerts
}

// matchesFilters checks if an alert matches any of the configured filters
func (a *AlertManagerAdmitter) matchesFilters(alert AlertManagerAlert) bool {
	for _, filter := range a.alertFilters {
		// Check if filter matches alert name
		if alertName, ok := alert.Labels["alertname"]; ok && alertName == filter {
			return true
		}

		// TODO: Add support for more complex label matching
		// For now, we only support exact alertname matching
	}

	return false
}

// getAlertNames extracts alert names from alerts for logging
func (a *AlertManagerAdmitter) getAlertNames(alerts []AlertManagerAlert) []string {
	var names []string
	for _, alert := range alerts {
		if name, ok := alert.Labels["alertname"]; ok {
			names = append(names, name)
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
		if alert.Status == "firing" && a.matchesFilters(alert) {
			alertName := a.getAlertName(alert)
			firingAlerts = append(firingAlerts, alertName)
		}
	}

	return firingAlerts, nil
}

// getAlertName extracts a meaningful name for the alert
func (a *AlertManagerAdmitter) getAlertName(alert AlertManagerAlert) string {
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
