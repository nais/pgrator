package main

import (
	"context"
	"crypto/tls"
	"os"
	"os/signal"
	"syscall"

	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller"
	"github.com/nais/pgrator/internal/synchronizer"
	pov1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/sethvargo/go-envconfig"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	_, err := liberator_scheme.AddAll(scheme)
	utilruntime.Must(err)

	err = pov1.AddToScheme(scheme)
	utilruntime.Must(err)

	err = acid_zalan_do_v1.AddToScheme(scheme)
	utilruntime.Must(err)
}

// nolint:gocyclo
func main() {
	ctx := context.Background()

	ctx, signalStop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer signalStop()

	opts := zap.Options{
		Development: false,
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := config.NewConfig(ctx, envconfig.OsLookuper())
	if err != nil {
		setupLog.Error(err, "unable to load configuration")
		os.Exit(1)
	}

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
		LeaderElection:         false,
		Client: client.Options{
			DryRun: &cfg.DryRun,
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	eb := k8sevents.NewEventBroadcasterAdapter(kubeClientset)
	// Run broadcaster for the lifetime of the process; no explicit shutdown
	eb.StartRecordingToSink(make(chan struct{}))
	recorder := eb.NewRecorder("pgrator")

	reconciler := &controller.PostgresReconciler{
		Config: cfg,
	}
	postgresController := synchronizer.NewSynchronizer(mgr.GetClient(), mgr.GetScheme(), reconciler, recorder)
	if err := postgresController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "postgresController", "Postgres")
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
