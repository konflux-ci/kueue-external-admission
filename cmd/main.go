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

package main

import (
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/konflux-ci/kueue-external-admission/internal/controller"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/enqueue"

	konfluxcidevv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	// +kubebuilder:scaffold:imports
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	// Import providers to register their factories
	_ "github.com/konflux-ci/kueue-external-admission/pkg/providers/all"

	admissionmanager "github.com/konflux-ci/kueue-external-admission/pkg/admission/manager"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(konfluxcidevv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
	utilruntime.Must(kueue.AddToScheme(scheme))
}

// ControllerFlags contains all command line flags for the application
type ControllerFlags struct {
	MetricsAddr          string
	MetricsCertPath      string
	MetricsCertName      string
	MetricsCertKey       string
	WebhookCertPath      string
	WebhookCertName      string
	WebhookCertKey       string
	EnableLeaderElection bool
	ProbeAddr            string
	SecureMetrics        bool
	EnableHTTP2          bool
	WatcherSyncPeriod    string
	LeaseDuration        time.Duration
	RenewDeadline        time.Duration
	RetryPeriod          time.Duration
	ZapOptions           *zap.Options
}

// AddFlags adds all CLI flags to the provided FlagSet
func (c *ControllerFlags) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.MetricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	fs.StringVar(&c.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&c.EnableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.BoolVar(&c.SecureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. "+
			"Use --metrics-secure=false to use HTTP instead.")
	fs.StringVar(&c.WebhookCertPath, "webhook-cert-path", "",
		"The directory that contains the webhook certificate.")
	fs.StringVar(&c.WebhookCertName, "webhook-cert-name", "tls.crt",
		"The name of the webhook certificate file.")
	fs.StringVar(&c.WebhookCertKey, "webhook-cert-key", "tls.key",
		"The name of the webhook key file.")
	fs.StringVar(&c.MetricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	fs.StringVar(&c.MetricsCertName, "metrics-cert-name", "tls.crt",
		"The name of the metrics server certificate file.")
	fs.StringVar(&c.MetricsCertKey, "metrics-cert-key", "tls.key",
		"The name of the metrics server key file.")
	fs.BoolVar(&c.EnableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	fs.StringVar(&c.WatcherSyncPeriod, "watcher-sync-period", "15s",
		"The amount of time the watcher will wait before checking for changes "+
			"(Go time.Duration format).")
	fs.DurationVar(&c.LeaseDuration, "leader-elect-lease-duration", 15*time.Second,
		"The duration that non-leader candidates will wait after observing a leadership "+
			"renewal until attempting to acquire leadership of a led but unrenewed leader slot. "+
			"This is effectively the maximum duration that a leader can be stopped before it "+
			"is replaced by another candidate.")
	fs.DurationVar(&c.RenewDeadline, "leader-elect-renew-deadline", 10*time.Second,
		"The interval between attempts by the acting master to renew a leadership slot "+
			"before it stops leading. This must be less than or equal to the lease duration.")
	fs.DurationVar(&c.RetryPeriod, "leader-elect-retry-period", 2*time.Second,
		"The duration the clients should wait between attempting acquisition and renewal "+
			"of a leadership.")

	c.ZapOptions = &zap.Options{
		Development: true,
	}
	c.ZapOptions.BindFlags(fs)
	config.RegisterFlags(fs)
}

// nolint:gocyclo
func main() {
	var tlsOpts []func(*tls.Config)

	// Initialize CLI flags
	cliFlags := &ControllerFlags{}
	fs := flag.NewFlagSet("main", flag.ExitOnError)
	cliFlags.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		setupLog.Error(err, "failed to parse command line flags")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(cliFlags.ZapOptions)))

	// log leader election configuration
	setupLog.Info("Leader election configuration",
		"lease-duration", cliFlags.LeaseDuration,
		"renew-deadline", cliFlags.RenewDeadline,
		"retry-period", cliFlags.RetryPeriod,
		"leader-election-enabled", cliFlags.EnableLeaderElection)

	// Note: watcher sync period not needed with new AlertManager approach

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !cliFlags.EnableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(cliFlags.WebhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", cliFlags.WebhookCertPath,
			"webhook-cert-name", cliFlags.WebhookCertName,
			"webhook-cert-key", cliFlags.WebhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(cliFlags.WebhookCertPath, cliFlags.WebhookCertName),
			filepath.Join(cliFlags.WebhookCertPath, cliFlags.WebhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'.
	// The Metrics options configure the server. More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   cliFlags.MetricsAddr,
		SecureServing: cliFlags.SecureMetrics,
		TLSOpts:       tlsOpts,
	}

	if cliFlags.SecureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in
		// 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/metrics/
		// filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for
	// development and testing, this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use
	//   certificates managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS
	//   certification.
	if len(cliFlags.MetricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", cliFlags.MetricsCertPath,
			"metrics-cert-name", cliFlags.MetricsCertName,
			"metrics-cert-key", cliFlags.MetricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(cliFlags.MetricsCertPath, cliFlags.MetricsCertName),
			filepath.Join(cliFlags.MetricsCertPath, cliFlags.MetricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts,
			func(config *tls.Config) {
				config.GetCertificate = metricsCertWatcher.GetCertificate
			})
	}
	// Log leader election configuration
	const leaderElectionId = "a7c8a3c7.konflux-ci.dev"
	if cliFlags.EnableLeaderElection {
		setupLog.Info("Leader election enabled with lease configuration",
			"lease-duration", cliFlags.LeaseDuration,
			"renew-deadline", cliFlags.RenewDeadline,
			"retry-period", cliFlags.RetryPeriod,
			"leader-election-id", leaderElectionId)
	} else {
		setupLog.Info("Leader election disabled")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: cliFlags.ProbeAddr,
		LeaderElection:         cliFlags.EnableLeaderElection,
		LeaderElectionID:       leaderElectionId,
		LeaseDuration:          &cliFlags.LeaseDuration,
		RenewDeadline:          &cliFlags.RenewDeadline,
		RetryPeriod:            &cliFlags.RetryPeriod,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this
		// significantly speeds up voluntary leader transitions as the new leader don't
		// have to wait LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform
		// cleanups after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	// Initialize the admission manager
	admissionManager := admissionmanager.NewManager(ctrl.Log.WithName("admission-manager"))
	// Add admission manager to manager so it starts/stops with the manager
	if err = mgr.Add(admissionManager); err != nil {
		setupLog.Error(err, "unable to add admission manager to manager")
		os.Exit(1)
	}

	// Setup signal handler context
	ctx := ctrl.SetupSignalHandler()

	// Setup field indexer for AdmissionCheck references to ExternalAdmissionConfig
	if err := controller.SetupIndexer(ctx, mgr.GetFieldIndexer()); err != nil {
		setupLog.Error(err, "unable to setup field indexer")
		os.Exit(1)
	}

	// Setup AdmissionCheck reconciler
	admissionCheckReconciler, err := controller.NewAdmissionCheckReconciler(
		mgr.GetClient(), mgr.GetScheme(), admissionManager)
	if err != nil {
		setupLog.Error(err, "unable to create AdmissionCheck controller")
		os.Exit(1)
	}
	if err = admissionCheckReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AdmissionCheck")
		os.Exit(1)
	}

	// Create event channel
	eventCh := make(chan event.GenericEvent)

	// Create enqueuer to watch for admission result changes
	enqueuer := enqueue.NewEnqueuer(
		admissionManager,
		eventCh,
		mgr.GetClient(),
		30*time.Second, // Check every 30 seconds
		ctrl.Log.WithName("enqueuer"),
	)

	// Add alert enqueuer to manager so it starts/stops with the manager
	if err = mgr.Add(enqueuer); err != nil {
		setupLog.Error(err, "unable to add enqueuer to manager")
		os.Exit(1)
	}

	// Set up the WorkloadReconciler with event channel
	workloadController := controller.NewWorkloadController(
		mgr.GetClient(),
		mgr.GetScheme(),
		admissionManager,
		clock.RealClock{},
	)
	if err = workloadController.SetupWithManager(mgr, eventCh); err != nil {
		setupLog.Error(err, "unable to create controller",
			"controller", "Workload")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
