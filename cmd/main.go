package main

import (
	"context"
	"crypto/tls"
	"os"
	"os/signal"
	"syscall"

	"github.com/nais/pgrator/internal/initscheme"
	"github.com/sethvargo/go-envconfig"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/events"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	initscheme.InitScheme(scheme)
}

// nolint:gocyclo
func main() {
	ctx := context.Background()

	ctx, signalStop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer signalStop()

	cfg, err := config.NewConfig(ctx, envconfig.OsLookuper())
	if err != nil {
		// Initialize a logger so we can log config errors because logging isn't configured until after config loaded
		zap.New().Error(err, "unable to load configuration")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(cfg.Development)))

	setupLog.Info("--- Configuration ---")
	cfg.Log(setupLog)
	setupLog.Info("---------------------")

	metricsServerOptions := metricsserver.Options{
		SecureServing: true,
		BindAddress:   ":8443",
		TLSOpts:       []func(*tls.Config){},
	}

	if len(cfg.MetricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", cfg.MetricsCertPath, "metrics-cert-name", "tls.crt", "metrics-cert-key", "tls.key")

		metricsServerOptions.CertDir = cfg.MetricsCertPath
		metricsServerOptions.CertName = "tls.crt"
		metricsServerOptions.KeyName = "tls.key"
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: ":8081",
		LeaderElection:         cfg.LeaderElectionEnabled,
		LeaderElectionID:       "pgrator.nais.io",
		Client: client.Options{
			DryRun: &cfg.DryRun,
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	recorder := events.NewRecorder(mgr.GetEventRecorder("pgrator"))

	postgresReconciler := &controller.PostgresReconciler{
		Config:   cfg,
		Recorder: recorder,
	}
	postgresController := synchronizer.NewSynchronizer(mgr.GetClient(), mgr.GetScheme(), postgresReconciler, recorder)
	if err := postgresController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "postgres")
		os.Exit(1)
	}

	valkeyReconciler := &controller.ValkeyReconciler{
		Aiven:    cfg.Aiven,
		Tenant:   cfg.Tenant,
		Recorder: recorder,
		Scheme:   scheme,
	}
	valkeyController := synchronizer.NewSynchronizer(mgr.GetClient(), mgr.GetScheme(), valkeyReconciler, recorder)
	if err := valkeyController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "valkey")
		os.Exit(1)
	}

	opensearchReconciler := &controller.OpenSearchReconciler{
		Aiven:    cfg.Aiven,
		Tenant:   cfg.Tenant,
		Recorder: recorder,
		Scheme:   scheme,
	}
	opensearchController := synchronizer.NewSynchronizer(mgr.GetClient(), mgr.GetScheme(), opensearchReconciler, recorder)
	if err := opensearchController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "opensearch")
		os.Exit(1)
	}

	if err := (&v1.OpenSearch{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "OpenSearch")
		os.Exit(1)
	}
	if err := (&v1.Valkey{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Valkey")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
