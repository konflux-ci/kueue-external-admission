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
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
)

func TestNewFactory(t *testing.T) {
	RegisterTestingT(t)
	factory := NewFactory()
	Expect(factory).ToNot(BeNil(), "Expected non-nil factory")
}

func TestFactory_NewAdmitter_AlertManagerProvider(t *testing.T) {
	RegisterTestingT(t)
	factory := NewFactory()
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
							AlertNames: []string{"TestAlert"},
						},
					},
					SyncConfig: &konfluxciv1alpha1.SyncConfig{
						Interval: &metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		},
	}

	admitter, err := factory.NewAdmitter(config, logr.Discard(), "test-admission-check")
	Expect(err).ToNot(HaveOccurred(), "Expected no error creating AlertManager admitter")
	Expect(admitter).ToNot(BeNil(), "Expected non-nil admitter")

	// Verify it implements the Admitter interface (this is guaranteed by the compiler)
	_ = admitter
}

func TestFactory_NewAdmitter_NoProviderConfigured(t *testing.T) {
	RegisterTestingT(t)
	factory := NewFactory()
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

	admitter, err := factory.NewAdmitter(config, logr.Discard(), "test-admission-check")
	Expect(err).To(HaveOccurred(), "Expected error when no provider is configured")
	Expect(admitter).To(BeNil(), "Expected nil admitter when error occurs")

	expectedErrMsg := "no supported provider configured in ExternalAdmissionConfig test-config"
	Expect(err.Error()).To(Equal(expectedErrMsg))
}

func TestFactory_NewAdmitter_NilConfig(t *testing.T) {
	RegisterTestingT(t)
	factory := NewFactory()
	admitter, err := factory.NewAdmitter(nil, logr.Discard(), "test-admission-check")
	Expect(err).To(HaveOccurred(), "Expected error when config is nil")
	Expect(admitter).To(BeNil(), "Expected nil admitter when error occurs")

	expectedErrMsg := "config cannot be nil"
	Expect(err.Error()).To(Equal(expectedErrMsg))
}
