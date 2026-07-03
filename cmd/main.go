package main

import (
	"flag"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	"github.com/custlynotts/private-dns-operator/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(dnsv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var corednsNamespace string
	var corednsConfigMap string
	var corednsDeployment string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&corednsNamespace, "coredns-namespace", envDefault("PRIVATE_DNS_COREDNS_NAMESPACE", "kube-system"), "Namespace containing the CoreDNS resources.")
	flag.StringVar(&corednsConfigMap, "coredns-configmap", envDefault("PRIVATE_DNS_COREDNS_CONFIGMAP", "coredns"), "CoreDNS ConfigMap name.")
	flag.StringVar(&corednsDeployment, "coredns-deployment", envDefault("PRIVATE_DNS_COREDNS_DEPLOYMENT", "coredns"), "CoreDNS Deployment name.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Client:                 client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.ConfigMap{}, &appsv1.Deployment{}}}},
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "private-dns-operator.dns.custlynotts.io",
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	reconciler := &controller.PrivateDNSZoneReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		CoreDNSNamespace:  corednsNamespace,
		CoreDNSConfigMap:  corednsConfigMap,
		CoreDNSDeployment: corednsDeployment,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "PrivateDNSZone")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctrl.Log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
