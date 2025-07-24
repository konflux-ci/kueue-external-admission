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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExternalAdmissionConfigSpec defines the desired state of ExternalAdmissionConfig.
type ExternalAdmissionConfigSpec struct {
	// AlertManager contains the AlertManager configuration
	AlertManager AlertManagerConfig `json:"alertManager"`

	// AlertFilters contains the configuration for filtering alerts
	AlertFilters AlertFiltersConfig `json:"alertFilters"`

	// Polling contains the configuration for polling intervals
	Polling PollingConfig `json:"polling,omitempty"`
}

// AlertManagerConfig contains AlertManager connection details
type AlertManagerConfig struct {
	// URL is the AlertManager API endpoint
	// +kubebuilder:validation:Required
	URL string `json:"url"`

	// Timeout for AlertManager API calls
	// +kubebuilder:default="10s"
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// TLS configuration for AlertManager connection
	TLS *TLSConfig `json:"tls,omitempty"`

	// BasicAuth configuration for AlertManager connection
	BasicAuth *BasicAuthConfig `json:"basicAuth,omitempty"`
}

// TLSConfig contains TLS configuration
type TLSConfig struct {
	// InsecureSkipVerify controls whether to skip certificate verification
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CAFile is the path to the CA certificate file
	CAFile string `json:"caFile,omitempty"`

	// CertFile is the path to the client certificate file
	CertFile string `json:"certFile,omitempty"`

	// KeyFile is the path to the client private key file
	KeyFile string `json:"keyFile,omitempty"`
}

// BasicAuthConfig contains basic authentication configuration
type BasicAuthConfig struct {
	// Username for basic authentication
	Username string `json:"username"`

	// PasswordSecret references a secret containing the password
	PasswordSecret SecretReference `json:"passwordSecret"`
}

// SecretReference references a secret
type SecretReference struct {
	// Name of the secret
	Name string `json:"name"`

	// Key within the secret
	Key string `json:"key"`
}

// AlertFiltersConfig contains alert filtering configuration
type AlertFiltersConfig struct {
	// AlertNames is a list of alert names to watch for
	AlertNames []string `json:"alertNames,omitempty"`

	// LabelSelectors for more complex alert filtering
	LabelSelectors []LabelSelector `json:"labelSelectors,omitempty"`
}

// LabelSelector contains label-based selection criteria
type LabelSelector struct {
	// Name of the label
	Name string `json:"name"`

	// Value of the label (supports regex)
	Value string `json:"value"`

	// Operator for comparison (equals, notEquals, regex)
	// +kubebuilder:validation:Enum=equals;notEquals;regex
	// +kubebuilder:default="equals"
	Operator string `json:"operator,omitempty"`
}

// PollingConfig contains polling configuration
type PollingConfig struct {
	// Interval between polls to AlertManager
	// +kubebuilder:default="30s"
	Interval *metav1.Duration `json:"interval,omitempty"`

	// FailureThreshold is the number of consecutive failures before marking as unhealthy
	// +kubebuilder:default=3
	FailureThreshold int `json:"failureThreshold,omitempty"`
}

// ExternalAdmissionConfigStatus defines the observed state of ExternalAdmissionConfig.
type ExternalAdmissionConfigStatus struct {
	// Conditions represent the latest available observations of the config's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is when the configuration was last successfully applied
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// ConnectedAdmissionChecks lists the AdmissionChecks using this config
	ConnectedAdmissionChecks []string `json:"connectedAdmissionChecks,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AlertManager URL",type=string,JSONPath=`.spec.alertManager.url`
// +kubebuilder:printcolumn:name="Alert Count",type=integer,JSONPath=`.spec.alertFilters.alertNames[*]`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExternalAdmissionConfig is the Schema for the externaladmissionconfigs API.
type ExternalAdmissionConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalAdmissionConfigSpec   `json:"spec,omitempty"`
	Status ExternalAdmissionConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ExternalAdmissionConfigList contains a list of ExternalAdmissionConfig.
type ExternalAdmissionConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalAdmissionConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalAdmissionConfig{}, &ExternalAdmissionConfigList{})
}
