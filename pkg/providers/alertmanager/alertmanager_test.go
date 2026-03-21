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
	"errors"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-openapi/runtime"
	. "github.com/onsi/gomega"
	"github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"
)

// mockAlertClient implements the alert.ClientService interface for testing
type mockAlertClient struct {
	getAlertsFunc func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error)
}

func (m *mockAlertClient) GetAlerts(
	params *alert.GetAlertsParams,
	opts ...alert.ClientOption,
) (*alert.GetAlertsOK, error) {
	if m.getAlertsFunc != nil {
		return m.getAlertsFunc(params, opts...)
	}
	return nil, errors.New("mock function not set")
}

func (m *mockAlertClient) PostAlerts(
	params *alert.PostAlertsParams,
	opts ...alert.ClientOption,
) (*alert.PostAlertsOK, error) {
	// Not needed for our tests, but required to implement the interface
	return nil, errors.New("PostAlerts not implemented in mock")
}

func (m *mockAlertClient) SetTransport(transport runtime.ClientTransport) {
	// Not needed for our tests, but required to implement the interface
}

func createTestAdmitter(
	alertFilters []string,
	mockFunc func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error),
) *admitter {
	mockClient := &mockAlertClient{
		getAlertsFunc: mockFunc,
	}

	apiClient := &client.AlertmanagerAPI{
		Alert: mockClient,
	}

	return &admitter{
		client:             apiClient,
		alertFilters:       alertFilters,
		logger:             logr.Discard(),
		admissionCheckName: "test-admission-check",
	}
}

func createActiveAlert(alertName string, state string) *models.GettableAlert {
	return &models.GettableAlert{
		Alert: models.Alert{
			Labels: models.LabelSet{
				"alertname": alertName,
			},
		},
		Status: &models.AlertStatus{
			State: &state,
		},
	}
}

func TestShouldAdmit_GetActiveAlertsError(t *testing.T) {
	RegisterTestingT(t)

	testError := errors.New("failed to get alerts")
	admitter := createTestAdmitter(
		[]string{"test-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption,
		) (*alert.GetAlertsOK, error) {
			return nil, testError
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).To(HaveOccurred(), "Expected error when getActiveAlerts fails")
	Expect(err.Error()).To(ContainSubstring("failed to query AlertManager"), "Expected wrapped error message")
	Expect(err.Error()).To(ContainSubstring("failed to get alerts"), "Expected original error message in wrapped error")
	Expect(result).ToNot(BeNil(), "Expected result to be non-nil even on error")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied on error")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
}

func TestShouldAdmit_NoAlertFiltersConfigured(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			// Return some alerts, but since no filters are configured, should admit anyway
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					createActiveAlert("some-alert", models.AlertStatusStateActive),
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(
		BeTrue(), "Expected admission to be allowed when no alert filters configured")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(BeEmpty(), "Expected no details when admitting by default")
}

func TestShouldAdmit_AlertFiltersConfiguredNoFiringAlerts(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			// Return alerts but none are firing or match our filters
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					createActiveAlert("other-alert", models.AlertStatusStateSuppressed),
					createActiveAlert("different-alert", models.AlertStatusStateUnprocessed),
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeTrue(),
		"Expected admission to be allowed when alert filters are configured and no active alerts match")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(BeEmpty(), "Expected no details when no firing alerts match")
}

func TestShouldAdmit_AlertFiltersConfiguredNonMatchingFiringAlerts(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			// Return firing alerts that don't match our filters
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					createActiveAlert("other-alert", models.AlertStatusStateActive),
					createActiveAlert("different-alert", models.AlertStatusStateActive),
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeTrue(),
		"Expected admission to be allowed when alert filters are configured and no active alerts match")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(BeEmpty(), "Expected no details when no alerts match filters")
}

func TestShouldAdmit_AlertFiltersConfiguredWithMatchingFiringAlerts(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert", "warning-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			// Return firing alerts that match our filters
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					createActiveAlert("critical-alert", models.AlertStatusStateActive), // Should match and be firing
					createActiveAlert("other-alert", models.AlertStatusStateActive),    // Firing but doesn't match filter
					createActiveAlert("warning-alert", models.AlertStatusStateActive),  // Should match and be firing
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied when matching alerts are firing")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(ContainElements("critical-alert", "warning-alert"),
		"Expected details to contain firing alert names")
	Expect(result.Details()).To(HaveLen(2), "Expected exactly 2 firing alerts in details")
}

func TestShouldAdmit_SingleMatchingFiringAlert(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					createActiveAlert("critical-alert", models.AlertStatusStateActive),
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied when matching alert is firing")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(ContainElement("critical-alert"), "Expected details to contain firing alert name")
	Expect(result.Details()).To(HaveLen(1), "Expected exactly 1 firing alert in details")
}

func TestShouldAdmit_AlertWithoutLabels(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			activeState := models.AlertStatusStateActive
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					{
						Alert: models.Alert{
							Labels: nil, // Alert without labels
						},
						Status: &models.AlertStatus{
							State: &activeState,
						},
					},
					createActiveAlert("critical-alert", activeState), // This should match
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied when matching alert is firing")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(ContainElement("critical-alert"), "Expected details to contain firing alert name")
	Expect(result.Details()).To(HaveLen(1),
		"Expected exactly 1 firing alert in details (alert without labels should be ignored)")
}

func TestShouldAdmit_AlertWithoutStatus(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			activeState := models.AlertStatusStateActive
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{
					{
						Alert: models.Alert{
							Labels: models.LabelSet{
								"alertname": "critical-alert",
							},
						},
						Status: nil, // Alert without status
					},
					createActiveAlert("critical-alert", activeState), // This should match
				},
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied when matching alert is firing")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(ContainElement("critical-alert"), "Expected details to contain firing alert name")
	Expect(result.Details()).To(HaveLen(1),
		"Expected exactly 1 firing alert in details (alert without status should be ignored)")
}

func TestShouldAdmit_EmptyAlertsResponse(t *testing.T) {
	RegisterTestingT(t)

	admitter := createTestAdmitter(
		[]string{"critical-alert"},
		func(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error) {
			return &alert.GetAlertsOK{
				Payload: []*models.GettableAlert{}, // Empty alerts response
			}, nil
		})

	result, err := admitter.shouldAdmit(context.Background())

	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(BeTrue(),
		"Expected admission to be allowed when alert filters are configured and no active alerts match")
	Expect(result.CheckName()).To(Equal("test-admission-check"))
	Expect(result.Details()).To(BeEmpty(), "Expected no details when no alerts are returned")
}
