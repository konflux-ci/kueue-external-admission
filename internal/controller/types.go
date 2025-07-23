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

package controller

import (
	"time"
)

// TODO: make the config more generic and allow for multiple external admission checker type

// AlertManagerAdmissionCheckConfig represents the configuration from AdmissionCheck parameters
type AlertManagerAdmissionCheckConfig struct {
	AlertManager AlertManagerConfig `yaml:"alertManager"`
	AlertFilters AlertFiltersConfig `yaml:"alertFilters"`
	Polling      PollingConfig      `yaml:"polling"`
}

type AlertManagerConfig struct {
	URL       string           `yaml:"url"`
	Timeout   time.Duration    `yaml:"timeout,omitempty"`
	TLS       *TLSConfig       `yaml:"tls,omitempty"`
	BasicAuth *BasicAuthConfig `yaml:"basicAuth,omitempty"`
}

type TLSConfig struct {
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify,omitempty"`
	CAFile             string `yaml:"caFile,omitempty"`
	CertFile           string `yaml:"certFile,omitempty"`
	KeyFile            string `yaml:"keyFile,omitempty"`
}

type BasicAuthConfig struct {
	Username       string          `yaml:"username"`
	PasswordSecret SecretReference `yaml:"passwordSecret"`
}

type SecretReference struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type AlertFiltersConfig struct {
	AlertNames     []string        `yaml:"alertNames,omitempty"`
	LabelSelectors []LabelSelector `yaml:"labelSelectors,omitempty"`
}

type LabelSelector struct {
	MatchLabels      map[string]string `yaml:"matchLabels,omitempty"`
	MatchExpressions []MatchExpression `yaml:"matchExpressions,omitempty"`
}

type MatchExpression struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"`
	Values   []string `yaml:"values"`
}

type PollingConfig struct {
	Interval         time.Duration `yaml:"interval,omitempty"`
	FailureThreshold int           `yaml:"failureThreshold,omitempty"`
}
