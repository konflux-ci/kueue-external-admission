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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	konfluxcidevv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/constant"
	"github.com/konflux-ci/kueue-external-admission/test/utils"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
)

// namespace where the project is deployed in
const namespace = "kueue-external-admission"

// serviceAccountName created for the project
const serviceAccountName = "kueue-external-admission-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "kueue-external-admission-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "kueue-external-admission-metrics-binding"

const metricsReaderClusterRoleName = "kueue-external-admission-metrics-reader"

// createGinkgoLogger creates a logger that writes to GinkgoWriter
func createGinkgoLogger() logr.Logger {
	// Create a zap logger that writes to GinkgoWriter
	config := zap.NewDevelopmentConfig()
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	// Create a custom core that writes to GinkgoWriter
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(config.EncoderConfig),
		zapcore.AddSync(GinkgoWriter),
		zapcore.DebugLevel,
	)

	zapLogger := zap.New(core).WithOptions(zap.AddCallerSkip(1))
	return zapr.NewLogger(zapLogger)
}

func backgroundPortForward(ctx context.Context, namespace, service, port string) context.CancelFunc {
	subCtx, subCtxCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(subCtx, "kubectl", "port-forward", "-n", namespace, service, port)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	go func() {
		defer GinkgoRecover()
		err := cmd.Run()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != -1 {
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Port forward failed: %d\n", exitErr.ExitCode()))
		}
	}()
	return subCtxCancel
}

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string
	var k8sClient client.Client
	var alertManagerClient *AlertManagerTestClient
	nsName := "test-ns"

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func(ctx context.Context) {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		projectImage := os.Getenv("IMG")
		Expect(projectImage).ToNot(Equal(""), "IMG environment variable must be declared")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("validating that the controller-manager pod is running as expected")
		verifyControllerUp := func(g Gomega) {
			// Get the name of the controller-manager pod
			cmd := exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)

			podOutput, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
			podNames := utils.GetNonEmptyLines(podOutput)
			g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
			controllerPodName = podNames[0]
			g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

			// Validate the pod's status
			cmd = exec.Command("kubectl", "get",
				"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
		}
		Eventually(verifyControllerUp).Should(Succeed())

		By("Creating a k8s client")
		// The context provided by the callback is closed when it's completed,
		// so we need to create another context for the client.
		k8sClient = getK8sClientOrDie(context.Background())

		By("Creating a AlertManager client")
		alertManagerPort := "9093"
		alertManagerURL := fmt.Sprintf("http://localhost:%s/api/v2", alertManagerPort)
		alertManagerClient, err = NewAlertManagerTestClient(
			alertManagerURL,
			createGinkgoLogger(),
		)
		Expect(err).NotTo(HaveOccurred())

		By("Starting port forward for AlertManager")
		alertManagerPortForwardCancel := backgroundPortForward(
			context.Background(),
			"monitoring",
			"service/alertmanager-operated",
			alertManagerPort,
		)
		DeferCleanup(alertManagerPortForwardCancel)

		By("Waiting for AlertManager to be ready")
		Eventually(func(g Gomega) {
			err := alertManagerClient.CheckConnection(ctx)
			g.Expect(err).NotTo(HaveOccurred())
		}).Should(Succeed())

		By(fmt.Sprintf("Creating a namespace: %s", nsName), func() {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Satisfy(func(err error) bool {
				return err == nil || kerrors.IsAlreadyExists(err)
			}))
		})
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager metrics", func() {
		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command(
				"kubectl",
				"create",
				"clusterrolebinding",
				"--dry-run=client",
				"-o",
				"yaml",
				metricsRoleBindingName,
				fmt.Sprintf("--clusterrole=%s", metricsReaderClusterRoleName),
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			crb, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to generate ClusterRoleBinding")

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(crb)
			Expect(utils.Run(cmd)).Error().NotTo(HaveOccurred(), "Failed to apply ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("validating that the ServiceMonitor for Prometheus is applied in the namespace")
			cmd = exec.Command("kubectl", "get", "ServiceMonitor", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "ServiceMonitor should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccount": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

	})

	Context("AlertManager admission check basic integration", func() {
		admissionCheckName := "test-alertmanager-check"
		var workload *kueue.Workload

		It("should apply test resources and verify the AdmissionCheck is Active", func(ctx context.Context) {

			By("applying test resources using server-side apply")
			cmd := exec.Command(
				"kubectl", "apply", "--server-side", "--field-manager=e2e-test",
				"--force-conflicts", "-f", "test/e2e/test-resources.yaml",
			)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply test resources: %s", output)
		})

		It("should verify the AdmissionCheck is Active", func(ctx context.Context) {
			Eventually(func(g Gomega) {
				var createdAdmissionCheck kueue.AdmissionCheck
				err := k8sClient.Get(ctx, client.ObjectKey{Name: admissionCheckName}, &createdAdmissionCheck)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(createdAdmissionCheck.Name).To(Equal(admissionCheckName))
				g.Expect(createdAdmissionCheck.Spec.ControllerName).To(Equal(constant.ControllerName))

				currentCondition := ptr.Deref(
					apimeta.FindStatusCondition(
						createdAdmissionCheck.Status.Conditions,
						kueue.AdmissionCheckActive,
					),
					metav1.Condition{},
				)
				g.Expect(currentCondition.Status).To(Equal(metav1.ConditionTrue))
			}).Should(Succeed())
		})

		It("should verify the ClusterQueue is Active", func(ctx context.Context) {
			By("verifying the ClusterQueue is Active")
			Eventually(func(g Gomega) {
				var createdClusterQueue kueue.ClusterQueue
				err := k8sClient.Get(ctx, client.ObjectKey{Name: "test-cluster-queue"}, &createdClusterQueue)
				g.Expect(err).NotTo(HaveOccurred())
				currentCondition := ptr.Deref(
					apimeta.FindStatusCondition(
						createdClusterQueue.Status.Conditions,
						kueue.ClusterQueueActive,
					),
					metav1.Condition{},
				)
				g.Expect(currentCondition.Status).To(Equal(metav1.ConditionTrue))
			}).Should(Succeed())
		})

		It("should create a Workload and verify the AdmissionCheck is Pending", func(ctx context.Context) {
			By("ensuring there is no silence for the test alert")
			Expect(alertManagerClient.DeleteSilencesByAlertName(ctx, "TestAlertAlwaysFiring")).To(Succeed())

			By("creating a Workload with generated name")
			workload = &kueue.Workload{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-workload-",
					Namespace:    nsName,
				},
				Spec: kueue.WorkloadSpec{
					QueueName: "test-local-queue",
					PodSets: []kueue.PodSet{
						{
							Name:  "main",
							Count: 1,
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:  "test-container",
											Image: "busybox",
											Resources: corev1.ResourceRequirements{
												Requests: corev1.ResourceList{
													"cpu":    resource.MustParse("100m"),
													"memory": resource.MustParse("128Mi"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workload)).To(Succeed())

			By("verifying the workload admission check status is pending")
			verifyWorkloadAdmissionCheckStatus(
				ctx, k8sClient, workload.Name, nsName, admissionCheckName,
				kueue.CheckStatePending, "denying workload",
			)
		})

		It("should verify Workload AdmissionCheck is Ready", func(ctx context.Context) {
			By("creating a silence for the test alert")
			silenceID, err := alertManagerClient.SilenceAlert(
				ctx,
				"TestAlertAlwaysFiring",
				5*time.Minute,
				"E2E test silence",
				nil,
			)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				err := alertManagerClient.DeleteSilence(ctx, silenceID)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("verifying the workload admission check status is ready")
			verifyWorkloadAdmissionCheckStatus(
				ctx, k8sClient, workload.Name, nsName, admissionCheckName,
				kueue.CheckStateReady, "approving workload",
			)

		})
	})
})

func getK8sClientOrDie(ctx context.Context) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kueue.AddToScheme(scheme))
	utilruntime.Must(konfluxcidevv1alpha1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()

	k8sCache, err := cache.New(cfg, cache.Options{Scheme: scheme, ReaderFailOnMissingInformer: true})
	Expect(err).ToNot(HaveOccurred(), "failed to create cache")

	_, err = k8sCache.GetInformer(ctx, &kueue.Workload{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for workloads")

	_, err = k8sCache.GetInformer(ctx, &kueue.AdmissionCheck{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for admission checks")

	_, err = k8sCache.GetInformer(ctx, &kueue.ClusterQueue{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for cluster queues")

	_, err = k8sCache.GetInformer(ctx, &kueue.LocalQueue{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for local queues")

	_, err = k8sCache.GetInformer(ctx, &konfluxcidevv1alpha1.ExternalAdmissionConfig{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for external admission configs")

	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			panic(err)
		}
	}()

	if synced := k8sCache.WaitForCacheSync(ctx); !synced {
		panic("failed waiting for cache to sync")
	}

	k8sClient, err := client.New(
		cfg,
		client.Options{
			Cache:  &client.CacheOptions{Reader: k8sCache},
			Scheme: scheme,
		},
	)
	Expect(err).ToNot(HaveOccurred(), "failed to create client")

	return k8sClient
}

// verifyWorkloadAdmissionCheckStatus verifies that a workload has the expected admission check status
func verifyWorkloadAdmissionCheckStatus(
	ctx context.Context, k8sClient client.Client, workloadName, namespace, admissionCheckName string,
	expectedState kueue.CheckState, expectedMessageSubstring string,
) {
	Eventually(func(g Gomega) {
		var createdWorkload kueue.Workload
		err := k8sClient.Get(ctx, client.ObjectKey{Name: workloadName, Namespace: namespace}, &createdWorkload)
		g.Expect(err).NotTo(HaveOccurred())

		var found bool
		for _, check := range createdWorkload.Status.AdmissionChecks {
			if check.Name == admissionCheckName {
				found = true
				g.Expect(check.State).To(Equal(expectedState))
				if expectedMessageSubstring != "" {
					g.Expect(check.Message).To(ContainSubstring(expectedMessageSubstring))
				}
				break
			}
		}
		g.Expect(found).To(BeTrue(), "AdmissionCheck should be present in workload status")
	}, 10*time.Minute).Should(Succeed())
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
