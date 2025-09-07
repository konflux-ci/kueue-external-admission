# kueue-external-admission

A Kubernetes controller that provides external admission checks for [Kueue](https://kueue.sigs.k8s.io/) workloads based on external conditions and monitoring systems.

## Description

The kueue-external-admission controller extends Kueue's admission control capabilities by allowing workloads to be admitted or denied based on external conditions. Currently, it integrates with Prometheus AlertManager to make admission decisions based on active alerts, enabling workload scheduling that considers cluster health and resource availability.

### Key Features

- **Event-driven Admission**: Workloads are automatically re-evaluated when external conditions change
- **AlertManager Integration**: Deny workload admission when selected alerts are active
- **Configurable Alert Filtering**: Fine-grained control over which alerts affect admission decisions
- **Multi-check Support**: Support for multiple admission checks with different configurations
- **Real-time Monitoring**: Continuous monitoring of external conditions with configurable sync intervals

### AlertManager Integration

The AlertManager provider enables workload scheduling based on cluster health with real-time alert monitoring, configurable alert filters, label-based filtering, authentication support, and custom sync intervals.

### Event-Driven Admission

The system automatically re-evaluates workloads when external conditions change, providing immediate response without manual intervention and efficient processing of only affected workloads.

### Multi-Check Support

Support for multiple admission checks with independent configurations, concurrent processing, aggregated results, and flexible deployment across different external systems.

### How It Works

1. **Configuration**: Create an `ExternalAdmissionConfig` resource specifying the external system to monitor
2. **AdmissionCheck**: Create a Kueue `AdmissionCheck` that references the configuration
3. **Monitoring**: The controller continuously monitors the external system (e.g., AlertManager for active alerts)
4. **Decision Making**: When a workload needs admission, the controller checks current external conditions
5. **Event-driven Updates**: When external conditions change, affected workloads are automatically re-evaluated

## Architecture

The system consists of several interconnected components:

1. **AdmissionCheck Controller**: Manages ExternalAdmissionConfig resources and creates appropriate admitters
2. **Workload Controller**: Processes Kueue workloads and applies admission decisions
3. **Admission Manager**: Coordinates multiple admitters and manages admission results
4. **Provider Factory**: Creates appropriate admitters based on provider configuration
5. **AlertManager Provider**: Connects to Prometheus AlertManager API and monitors active alerts

### Event-Driven Architecture

The system uses an event-driven approach to ensure workloads are re-evaluated when external conditions change:

1. **Continuous Monitoring**: Admitters continuously monitor external systems
2. **Event Generation**: When conditions change, events are generated
3. **Workload Re-evaluation**: Affected workloads are automatically re-evaluated
4. **Status Updates**: Workload admission states are updated accordingly

## Getting Started

### Prerequisites
- Go version v1.24.0+
- Docker version 17.03+ or Podman
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster
- [Kueue](https://kueue.sigs.k8s.io/) installed in your cluster
- (Optional) Prometheus and AlertManager for AlertManager provider functionality

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/kueue-external-admission:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/kueue-external-admission:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/samples:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

## Configuration

### ExternalAdmissionConfig

The `ExternalAdmissionConfig` resource defines how the controller should connect to external systems and what conditions to monitor.

#### AlertManager Provider Example

```yaml
apiVersion: konflux-ci.dev/v1alpha1
kind: ExternalAdmissionConfig
metadata:
  name: alertmanager-config
  namespace: default
spec:
  provider:
    alertManager:
      connection:
        url: "http://alertmanager-operated.monitoring.svc.cluster.local:9093"
        timeout: "10s"
        # Optional: Bearer token authentication
        # bearerToken:
        #   tokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token"
      alertFilters:
        - alertNames:
            - "HighCPUUsage"
            - "HighMemoryUsage"
            - "NodeNotReady"
          labelSelectors:
            - name: "severity"
              value: "critical"
              operator: "equals"
        - alertNames:
            - "KubernetesPodCrashLooping"
            - "DiskSpaceRunningLow"
      syncConfig:
        interval: "30s"
```

#### AdmissionCheck Configuration

```yaml
apiVersion: kueue.x-k8s.io/v1beta1
kind: AdmissionCheck
metadata:
  name: sample-admission-check
spec:
  controllerName: konflux-ci.dev/kueue-external-admission
  parameters:
    apiGroup: konflux-ci.dev
    kind: ExternalAdmissionConfig
    name: alertmanager-config
```

### Configuration Options

#### AlertManager Provider

- **connection.url**: AlertManager API endpoint URL
- **connection.timeout**: Timeout for API calls (default: 10s)
- **connection.bearerToken.tokenFile**: Path to file containing bearer token for authentication
- **alertFilters**: List of alert filtering configurations
  - **alertNames**: List of alert names to monitor
  - **labelSelectors**: Label-based filtering with operators (equals, notEquals, regex)
- **syncConfig.interval**: How often to check external conditions (default: 30s)

## Usage Examples

### Basic Workflow

1. **Create ExternalAdmissionConfig**: Define the external system to monitor
2. **Create AdmissionCheck**: Reference the configuration in a Kueue admission check
3. **Configure ClusterQueue**: Add the admission check to your ClusterQueue
4. **Submit Workloads**: Workloads will be automatically evaluated against external conditions

### Example: Complete Setup

```yaml
# 1. Create ExternalAdmissionConfig
apiVersion: konflux-ci.dev/v1alpha1
kind: ExternalAdmissionConfig
metadata:
  name: cluster-health-check
spec:
  provider:
    alertManager:
      connection:
        url: "http://alertmanager.monitoring.svc.cluster.local:9093"
      alertFilters:
        - alertNames:
            - "HighCPUUsage"
            - "HighMemoryUsage"
            - "NodeNotReady"
      syncConfig:
        interval: "30s"
---
# 2. Create AdmissionCheck
apiVersion: kueue.x-k8s.io/v1beta1
kind: AdmissionCheck
metadata:
  name: cluster-health-check
spec:
  controllerName: konflux-ci.dev/kueue-external-admission
  parameters:
    apiGroup: konflux-ci.dev
    kind: ExternalAdmissionConfig
    name: cluster-health-check
---
# 3. Configure ClusterQueue
apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  name: my-cluster-queue
spec:
  admissionChecks:
    - cluster-health-check
  # ... other ClusterQueue configuration
```

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/kueue-external-admission:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

## Development

### Building and Testing

```bash
# Build the controller
make build

# Run tests
make test

# Run linting
make lint

# Run end-to-end tests (requires Kind cluster)
make test-e2e
```

### Adding New Providers

The system is designed to be extensible. To add a new provider:

1. **Create Provider Implementation**: Implement the Admitter interface
2. **Update Factory**: Add the new provider to the factory
3. **Add API Types**: Extend ExternalAdmissionConfigSpec with new provider configuration
4. **Add Tests**: Create comprehensive tests for the new provider

## Contributing

We welcome contributions! Please follow these guidelines:

1. **Fork and Clone**: Fork the repository and clone your fork
2. **Create Branch**: Create a feature branch for your changes
3. **Make Changes**: Implement your changes with appropriate tests
4. **Run Tests**: Ensure all tests pass (`make test`)
5. **Run Linting**: Fix any linting issues (`make lint`)
6. **Submit PR**: Create a pull request with a clear description

### Development Setup

1. **Install Dependencies**: Ensure you have Go 1.24+, Docker/Podman, and kubectl
2. **Install Kueue**: Deploy Kueue to your development cluster
3. **Run Locally**: Use `make run` to run the controller locally
4. **Test Changes**: Use `make test-e2e` for end-to-end testing

### Code Style

- Follow Go conventions and best practices
- Add comprehensive tests for new features
- Update documentation for API changes
- Use meaningful commit messages

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

