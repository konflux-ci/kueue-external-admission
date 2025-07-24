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

package watcher

import (
	"fmt"

	"github.com/go-logr/logr"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
)


// AdmitterFactory is a function type for creating admitters
type AdmitterFactory func(*konfluxciv1alpha1.ExternalAdmissionConfig, logr.Logger) (Admitter, error)

// providerFactories holds the registered provider factories
var providerFactories = make(map[string]AdmitterFactory)

// RegisterProviderFactory registers a factory function for a provider type
func RegisterProviderFactory(providerType string, factory AdmitterFactory) {
	providerFactories[providerType] = factory
}

// NewAdmitter creates an Admitter based on the provider configuration in ExternalAdmissionConfig
func NewAdmitter(config *konfluxciv1alpha1.ExternalAdmissionConfig, logger logr.Logger) (Admitter, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	provider := config.Spec.Provider

	// Check which provider is configured and use registered factory
	switch {
	case provider.AlertManager != nil:
		if factory, exists := providerFactories["alertmanager"]; exists {
			return factory(config, logger)
		}
		return nil, fmt.Errorf("alertmanager provider factory not registered")
	default:
		return nil, fmt.Errorf("no supported provider configured in ExternalAdmissionConfig %s/%s",
			config.Namespace, config.Name)
	}
}
