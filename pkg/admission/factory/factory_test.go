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

package factory

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

func init() {
	// Register a mock AlertManager factory for testing
	RegisterProviderFactory("alertmanager",
		func(
			config *konfluxciv1alpha1.ExternalAdmissionConfig,
			logger logr.Logger,
			admissionCheckName string,
		) (admission.Admitter, error) {
			// Return a simple mock admitter for testing
			return &mockTestAdmitter{}, nil
		})
}

// mockTestAdmitter is a simple mock for factory testing
type mockTestAdmitter struct{}

func (m *mockTestAdmitter) ShouldAdmit(ctx context.Context) (result.AggregatedAdmissionResult, error) {
	builder := result.NewAggregatedAdmissionResultBuilder()
	return builder.Build(), nil
}

func (m *mockTestAdmitter) Sync(ctx context.Context, asyncAdmissionResults chan<- result.AsyncAdmissionResult) error {
	return nil
}

func TestNewAdmitter_AlertManagerProvider(t *testing.T) {
	RegisterTestingT(t)
	config := &konfluxciv1alpha1.ExternalAdmissionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
			Provider: konfluxciv1alpha1.ProviderConfig{
				AlertManager: &konfluxciv1alpha1.AlertManagerProviderConfig{
					Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
						URL: "http://test-alertmanager:9093",
					},
					AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
						{
							AlertNames: []string{"test-alert"},
						},
					},
				},
			},
		},
	}

	admitter, err := NewAdmitter(config, logr.Discard(), "test-admission-check")
	Expect(err).ToNot(HaveOccurred(), "Expected no error creating AlertManager admitter")
	Expect(admitter).ToNot(BeNil(), "Expected non-nil admitter")

	// Verify it implements the Admitter interface (this is guaranteed by the compiler)
	_ = admitter
}

func TestNewAdmitter_NoProviderConfigured(t *testing.T) {
	RegisterTestingT(t)
	config := &konfluxciv1alpha1.ExternalAdmissionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
			Provider: konfluxciv1alpha1.ProviderConfig{
				// No provider configured
			},
		},
	}

	admitter, err := NewAdmitter(config, logr.Discard(), "test-admission-check")
	Expect(err).To(HaveOccurred(), "Expected error when no provider is configured")
	Expect(admitter).To(BeNil(), "Expected nil admitter when error occurs")

	expectedErrMsg := "no supported provider configured in ExternalAdmissionConfig test-config"
	Expect(err.Error()).To(Equal(expectedErrMsg))
}

func TestNewAdmitter_NilConfig(t *testing.T) {
	RegisterTestingT(t)
	admitter, err := NewAdmitter(nil, logr.Discard(), "test-admission-check")
	Expect(err).To(HaveOccurred(), "Expected error when config is nil")
	Expect(admitter).To(BeNil(), "Expected nil admitter when error occurs")

	expectedErrMsg := "config cannot be nil"
	Expect(err.Error()).To(Equal(expectedErrMsg))
}
