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
	"fmt"

	"github.com/go-logr/logr"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/providers/alertmanager"
)

// Factory is a struct-based factory for creating admitters based on provider configuration.
// This provides a simple, straightforward approach to instantiate providers without
// implicit registration or complex patterns.
type Factory struct {
	// Future configuration options can be added here, such as:
	// - Default timeouts
	// - Common client configurations
	// - Metrics collectors
	// - etc.
}

// NewFactory creates a new Factory instance.
func NewFactory() *Factory {
	return &Factory{}
}

// NewAdmitter creates an Admitter based on the provider configuration in ExternalAdmissionConfig.
// This is a simple, straightforward factory that directly instantiates providers without
// implicit registration or complex patterns.
func (f *Factory) NewAdmitter(
	config *konfluxciv1alpha1.ExternalAdmissionConfig,
	logger logr.Logger,
	admissionCheckName string,
) (admission.Admitter, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	provider := config.Spec.Provider

	// Directly check which provider is configured and create the appropriate admitter
	switch {
	case provider.AlertManager != nil:
		return alertmanager.NewAdmitter(
			provider.AlertManager,
			logger.WithName("provider.alertmanager"),
			admissionCheckName,
		)
	default:
		return nil, fmt.Errorf("no supported provider configured in ExternalAdmissionConfig %s", config.Name)
	}

	// Future providers can be added here as new case statements, for example:
	// case provider.Webhook != nil:
	//     return webhook.NewAdmitter(
	//         provider.Webhook,
	//         logger.WithName("provider.webhook"),
	//         admissionCheckName,
	//         // webhook-specific arguments can be added here
	//     )
	// case provider.CustomScript != nil:
	//     return customscript.NewAdmitter(
	//         provider.CustomScript,
	//         logger.WithName("provider.customscript"),
	//         admissionCheckName,
	//         // custom script-specific arguments can be added here
	//     )
}
