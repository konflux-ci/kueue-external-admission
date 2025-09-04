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

// Package e2e provides AlertManager test client functionality for end-to-end tests.
// This includes silence management and can be extended with other AlertManager operations.
package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-openapi/strfmt"
	alertclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/silence"
	"github.com/prometheus/alertmanager/api/v2/models"

	"github.com/konflux-ci/kueue-external-admission/pkg/providers/alertmanager"
	"k8s.io/utils/ptr"
)

// AlertManagerTestClient provides AlertManager functionality for e2e tests.
// Currently includes silence management but can be extended with other AlertManager operations
// such as alert querying, configuration management, etc.
type AlertManagerTestClient struct {
	client *alertclient.AlertmanagerAPI
	logger logr.Logger
}

// NewAlertManagerTestClient creates a new AlertManager client for e2e tests
func NewAlertManagerTestClient(alertManagerURL string, logger logr.Logger) (*AlertManagerTestClient, error) {
	// Use the existing client creation function from the provider
	client, err := alertmanager.NewAlertManagerClient(alertManagerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AlertManager client: %w", err)
	}

	return &AlertManagerTestClient{
		client: client,
		logger: logger,
	}, nil
}

// SilenceAlert creates a silence for a specific alert in AlertManager
// It takes an alert name, duration, comment, and optional additional matchers
// Returns the silence ID if successful, or an error if the operation fails
func (c *AlertManagerTestClient) SilenceAlert(
	ctx context.Context,
	alertName string,
	duration time.Duration,
	comment string,
	additionalMatchers map[string]string,
) (string, error) {
	// Calculate start and end times
	now := time.Now()
	endsAt := now.Add(duration)

	// Create the basic matcher for the alert name
	matchers := models.Matchers{
		{
			Name:    &alertName,
			Value:   &alertName,
			IsRegex: ptr.To(false), // Exact match, not regex
			IsEqual: ptr.To(true),  // Equal match
		},
	}

	// Add additional matchers if provided
	for key, value := range additionalMatchers {
		matchers = append(matchers, &models.Matcher{
			Name:    &key,
			Value:   &value,
			IsRegex: ptr.To(false), // Exact match, not regex
			IsEqual: ptr.To(true),  // Equal match
		})
	}

	// Create the silence object
	postableSilence := &models.PostableSilence{
		Silence: models.Silence{
			Comment:   &comment,
			CreatedBy: ptr.To("e2e-test"),
			StartsAt:  ptr.To(strfmt.DateTime(now)),
			EndsAt:    ptr.To(strfmt.DateTime(endsAt)),
			Matchers:  matchers,
		},
	}

	// Create the API parameters
	params := silence.NewPostSilencesParamsWithContext(ctx).WithSilence(postableSilence)

	// Call the AlertManager API to create the silence
	resp, err := c.client.Silence.PostSilences(params)
	if err != nil {
		return "", fmt.Errorf("failed to create silence in AlertManager: %w", err)
	}

	// Extract the silence ID from the response
	if resp.Payload == nil || resp.Payload.SilenceID == "" {
		return "", fmt.Errorf("AlertManager returned empty silence ID")
	}

	c.logger.Info("Successfully created silence in AlertManager",
		"alertName", alertName,
		"silenceID", resp.Payload.SilenceID,
		"duration", duration,
		"comment", comment,
	)

	return resp.Payload.SilenceID, nil
}

// SilenceAlertByLabels creates a silence for alerts matching specific labels
// This is more flexible than SilenceAlert as it allows matching on any label combination
func (c *AlertManagerTestClient) SilenceAlertByLabels(
	ctx context.Context,
	matchers map[string]string,
	duration time.Duration,
	comment string,
) (string, error) {
	if len(matchers) == 0 {
		return "", fmt.Errorf("at least one matcher is required")
	}

	// Calculate start and end times
	now := time.Now()
	endsAt := now.Add(duration)

	// Create matchers from the provided labels
	alertMatchers := make(models.Matchers, 0, len(matchers))
	for key, value := range matchers {
		alertMatchers = append(alertMatchers, &models.Matcher{
			Name:    &key,
			Value:   &value,
			IsRegex: ptr.To(false), // Exact match, not regex
			IsEqual: ptr.To(true),  // Equal match
		})
	}

	// Create the silence object
	postableSilence := &models.PostableSilence{
		Silence: models.Silence{
			Comment:   &comment,
			CreatedBy: ptr.To("e2e-test"),
			StartsAt:  ptr.To(strfmt.DateTime(now)),
			EndsAt:    ptr.To(strfmt.DateTime(endsAt)),
			Matchers:  alertMatchers,
		},
	}

	// Create the API parameters
	params := silence.NewPostSilencesParamsWithContext(ctx).WithSilence(postableSilence)

	// Call the AlertManager API to create the silence
	resp, err := c.client.Silence.PostSilences(params)
	if err != nil {
		return "", fmt.Errorf("failed to create silence in AlertManager: %w", err)
	}

	// Extract the silence ID from the response
	if resp.Payload == nil || resp.Payload.SilenceID == "" {
		return "", fmt.Errorf("AlertManager returned empty silence ID")
	}

	c.logger.Info("Successfully created silence in AlertManager",
		"matchers", matchers,
		"silenceID", resp.Payload.SilenceID,
		"duration", duration,
		"comment", comment,
	)

	return resp.Payload.SilenceID, nil
}

// DeleteSilence removes a silence from AlertManager by its ID
func (c *AlertManagerTestClient) DeleteSilence(ctx context.Context, silenceID string) error {
	params := silence.NewDeleteSilenceParamsWithContext(ctx).WithSilenceID(strfmt.UUID(silenceID))

	_, err := c.client.Silence.DeleteSilence(params)
	if err != nil {
		return fmt.Errorf("failed to delete silence %s from AlertManager: %w", silenceID, err)
	}

	c.logger.Info("Successfully deleted silence from AlertManager",
		"silenceID", silenceID,
	)

	return nil
}

// GetSilences retrieves all active silences from AlertManager
func (c *AlertManagerTestClient) GetSilences(ctx context.Context) ([]*models.GettableSilence, error) {
	params := silence.NewGetSilencesParamsWithContext(ctx)

	resp, err := c.client.Silence.GetSilences(params)
	if err != nil {
		return nil, fmt.Errorf("failed to get silences from AlertManager: %w", err)
	}

	return resp.Payload, nil
}

// DeleteSilencesByAlertName deletes all silences that match the given alert name
// This is useful for cleaning up silences for specific alerts in e2e tests
func (c *AlertManagerTestClient) DeleteSilencesByAlertName(ctx context.Context, alertName string) error {
	// Get all active silences
	silences, err := c.GetSilences(ctx)
	if err != nil {
		return fmt.Errorf("failed to get silences: %w", err)
	}

	var deletedCount int
	var errors []error

	// Find and delete silences that match the alert name
	for _, silence := range silences {
		if silence == nil || silence.Status == nil {
			continue
		}

		// Check if this silence matches the alert name
		if c.silenceMatchesAlertName(silence, alertName) {
			if silence.ID != nil {
				err := c.DeleteSilence(ctx, *silence.ID)
				if err != nil {
					errors = append(errors, fmt.Errorf("failed to delete silence %s: %w", *silence.ID, err))
				} else {
					deletedCount++
					c.logger.Info("Deleted silence for alert", "alertName", alertName, "silenceID", *silence.ID)
				}
			}
		}
	}

	// Log summary
	c.logger.Info("Completed deletion of silences by alert name",
		"alertName", alertName,
		"deletedCount", deletedCount,
		"errorCount", len(errors),
	)

	// Return error if any deletions failed
	if len(errors) > 0 {
		return fmt.Errorf("failed to delete %d out of %d silences for alert %s: %v",
			len(errors), deletedCount+len(errors), alertName, errors)
	}

	return nil
}

// silenceMatchesAlertName checks if a silence matches the given alert name
func (c *AlertManagerTestClient) silenceMatchesAlertName(silence *models.GettableSilence, alertName string) bool {
	if silence == nil || silence.Matchers == nil {
		return false
	}

	// Check if any of the silence's matchers match the alert name
	for _, matcher := range silence.Matchers {
		if matcher != nil && matcher.Name != nil && matcher.Value != nil {
			// Check if this matcher is for the alertname label with the target value
			if *matcher.Name == "alertname" && *matcher.Value == alertName {
				return true
			}
		}
	}

	return false
}
