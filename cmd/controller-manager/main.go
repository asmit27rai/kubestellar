/*
Copyright 2023 The KubeStellar Authors.

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

// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
// to ensure that exec-entrypoint and run can make use of them.

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"net/http"
	"context"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/component-base/metrics/legacyregistry"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	clientopts "github.com/kubestellar/kubestellar/options"
	"github.com/kubestellar/kubestellar/pkg/binding"
	ksctlr "github.com/kubestellar/kubestellar/pkg/controller"
	"github.com/kubestellar/kubestellar/pkg/ctrlutil"
	ksmetrics "github.com/kubestellar/kubestellar/pkg/metrics"
	"github.com/kubestellar/kubestellar/pkg/status"
	"github.com/kubestellar/kubestellar/pkg/util"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	scheme = runtime.NewScheme()
)

const (
	// number of workers to run the reconciliation loop
	workers = 4
	otelMetricsPort = ":2222"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	processOpts := clientopts.ProcessOptions{
		MetricsBindAddr:     ":8080",
		HealthProbeBindAddr: ":8081",
		PProfBindAddr:       ":8082",
	}
	var enableLeaderElection bool
	var itsName string
	var wdsName string
	var allowedGroupsString string
	var controllers []string
	var metricsPort string
	var monitoredNamespace string
	var monitoredDeployment string
	var bindingName string
	var wecContext string
	pflag.StringVar(&wecContext, "wec-context", "", "Context name for WEC cluster in kubeconfig")
	pflag.StringVar(&bindingName, "binding-name", "", "Name of the binding policy for the monitored deployment")
	pflag.StringVar(&monitoredNamespace, "monitored-namespace", "default", "Namespace of the deployment to monitor")
	pflag.StringVar(&monitoredDeployment, "monitored-deployment", "", "Name of the deployment to monitor")
	pflag.StringVar(&metricsPort, "metrics-port", otelMetricsPort, "Port for exposing OpenTelemetry metrics")
	pflag.StringVar(&itsName, "its-name", "", "name of the Inventory and Transport Space to connect to (empty string means to use the only one)")
	pflag.StringVar(&wdsName, "wds-name", "", "name of the workload description space to connect to")
	pflag.StringVar(&allowedGroupsString, "api-groups", "", "list of allowed api groups, comma separated. Empty string means all API groups are allowed")
	pflag.StringSliceVar(&controllers, "controllers", []string{}, "list of controllers to be started by the controller manager, lower case and comma separated, e.g. 'binding,status'. If not specified (or emtpy list specifed), all controllers are started. Currently available controllers are 'binding' and 'status'.")
	pflag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	itsClientLimits := clientopts.NewClientLimits[*pflag.FlagSet]("its", "accessing the ITS")
	wdsClientLimits := clientopts.NewClientLimits[*pflag.FlagSet]("wds", "accessing the WDS")
	processOpts.AddToFlags(pflag.CommandLine)
	itsClientLimits.AddFlags(pflag.CommandLine)
	wdsClientLimits.AddFlags(pflag.CommandLine)
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	ctx, _ := ksctlr.InitialContext()
	logger := klog.FromContext(ctx)
	ctrl.SetLogger(logger)
	setupLog := logger.WithName("setup")

	pflag.VisitAll(func(flg *pflag.Flag) {
		setupLog.Info("Command line flag", "name", flg.Name, "value", flg.Value)
	})

	// parse allowed resources string
	allowedGroupsSet := util.ParseAPIGroupsString(allowedGroupsString)

	// check controllers flag
	ctlrsToStart := sets.New(controllers...)
	if !sets.New(
		strings.ToLower(binding.ControllerName),
		strings.ToLower(status.ControllerName),
	).IsSuperset(ctlrsToStart) {
		setupLog.Error(fmt.Errorf("unkown controller specified"), "'controllers' flag has incorrect value")
		os.Exit(1)
	}

	ksctlr.Start(ctx, processOpts)

	spacesClientMetrics := ksmetrics.NewMultiSpaceClientMetrics()
	ksmetrics.MustRegister(legacyregistry.Register, spacesClientMetrics)
	wdsClientMetrics := spacesClientMetrics.MetricsForSpace("wds")
	itsClientMetrics := spacesClientMetrics.MetricsForSpace("its")

	// TODO: engage leader election if requested

	// get the config for WDS
	setupLog.Info("Getting config for WDS", "name", wdsName)
	wdsRestConfig, wdsName, err := ctrlutil.GetWDSKubeconfig(setupLog, wdsName)
	if err != nil {
		setupLog.Error(err, "unable to get WDS kubeconfig")
		os.Exit(1)
	}
	setupLog.Info("Got config for WDS", "name", wdsName)
	wdsRestConfig = wdsClientLimits.LimitConfig(wdsRestConfig)

	// get the config for ITS
	setupLog.Info("Getting config for ITS")
	itsRestConfig, itsName, err := ctrlutil.GetITSKubeconfig(setupLog, itsName)
	if err != nil {
		setupLog.Error(err, "unable to get ITS kubeconfig")
		os.Exit(1)
	}
	setupLog.Info("Got config for ITS", "name", itsName)
	itsRestConfig = itsClientLimits.LimitConfig(itsRestConfig)

	// TODO: set wecRestConfig, monitoredNamespace, monitoredDeployment as needed

	// Create latency collector
	if monitoredDeployment != "" && bindingName != "" {
		var wecRestConfig *rest.Config
		var err error
		
		if wecContext != "" {
			// Use default kubeconfig loading rules
			loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
			// Explicitly set kubeconfig path if provided via flag
			if flag := pflag.Lookup("kubeconfig"); flag != nil && flag.Value.String() != "" {
				loadingRules.ExplicitPath = flag.Value.String()
			}
			
			configOverrides := &clientcmd.ConfigOverrides{CurrentContext: wecContext}
			
			wecRestConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				loadingRules,
				configOverrides,
			).ClientConfig()
			if err != nil {
				setupLog.Error(err, "failed to create WEC config")
				// Fall back to ITS config
				wecRestConfig = itsRestConfig
			}
		} else {
			setupLog.Info("Using ITS config for WEC as fallback")
			wecRestConfig = itsRestConfig
		}
		
		latencyCollector := createLatencyCollector(
			wdsRestConfig, itsRestConfig, wecRestConfig,
			monitoredNamespace, monitoredDeployment, bindingName, monitoredNamespace,
		)
		legacyregistry.MustRegister(latencyCollector)
	}

	// Start metrics server
	go func() {
		setupLog.Info("Starting metrics server", "port", metricsPort)
		metricsServer := &http.Server{
			Addr:    metricsPort,
			Handler: legacyregistry.Handler(),
		}
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			setupLog.Error(err, "Metrics server failed")
		}
	}()

	workloadEventRelay := &workloadEventRelay{}

	// create the binding controller
	bindingController, err := binding.NewController(logger, wdsClientMetrics, itsClientMetrics, wdsRestConfig, itsRestConfig, wdsName, allowedGroupsSet, workloadEventRelay)
	if err != nil {
		setupLog.Error(err, "unable to create binding controller")
		os.Exit(1)
	}

	if err := bindingController.EnsureCRDs(ctx); err != nil {
		setupLog.Error(err, "error installing the CRDs")
		os.Exit(1)
	}

	if err := bindingController.AppendKSResources(ctx); err != nil {
		setupLog.Error(err, "error appending KubeStellar resources to discovered lists")
		os.Exit(1)
	}
	
	// Add context cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startBindingController := len(ctlrsToStart) == 0 || ctlrsToStart.Has(strings.ToLower(binding.ControllerName))
	startStatusCtlr := len(ctlrsToStart) == 0 || ctlrsToStart.Has(strings.ToLower(status.ControllerName))
	var statusController *status.Controller

	if startStatusCtlr {
		if !startBindingController {
			setupLog.Error(nil, "Status controller does not work without binding controller")
			os.Exit(1)
		}
		// check if status add-on present before starting the status controller
		for i := 1; true; i++ {
			if util.CheckWorkStatusPresence(itsRestConfig) {
				break
			}
			if (i & (i - 1)) == 0 {
				setupLog.Info("Not creating status controller yet because WorkStatus is not defined in the ITS")
			}
			i++
			time.Sleep(15 * time.Second)
		}
		setupLog.Info("Creating controller", "name", status.ControllerName)
		statusController, err = status.NewController(logger, wdsClientMetrics, itsClientMetrics, wdsRestConfig, itsRestConfig, wdsName,
			bindingController.GetBindingPolicyResolver())
		if err != nil {
			setupLog.Error(err, "unable to create status controller")
			os.Exit(1)
		}
		workloadEventRelay.statusController = statusController
	} else {
		setupLog.Info("Not creating status controller")
	}

	cListers := make(chan interface{}, 1)

	if startBindingController {
		setupLog.Info("Starting controller", "name", binding.ControllerName)
		if err := bindingController.Start(ctx, workers, cListers); err != nil {
			setupLog.Error(err, "error starting the binding controller")
			os.Exit(1)
		}
	}

	if startStatusCtlr {
		setupLog.Info("Starting controller", "name", status.ControllerName)
		if err := statusController.Start(ctx, workers, cListers); err != nil {
			setupLog.Error(err, "error starting the status controller")
			os.Exit(1)
		}
	}

	select {}
}

// workloadEventRelay implements binding.WorkloadEventHandler and relays the notifications
// to the status controller.
type workloadEventRelay struct {
	statusController *status.Controller
}

var _ binding.WorkloadEventHandler = &workloadEventRelay{}

func (wer *workloadEventRelay) HandleWorkloadObjectEvent(gvr schema.GroupVersionResource, oldObj, obj util.MRObject, eventType binding.WorkloadEventType, wasDeletedFinalStateUnknown bool) {
	if wer.statusController != nil {
		wer.statusController.HandleWorkloadObjectEvent(gvr, oldObj, obj, eventType, wasDeletedFinalStateUnknown)
	}
}

func createLatencyCollector(
    wdsCfg, itsCfg, wecCfg *rest.Config,
    namespace, deployment, bindingName, itsNamespace string,
) *ksmetrics.LatencyCollector {
    wdsClient := kubernetes.NewForConfigOrDie(wdsCfg)
    wecClient := kubernetes.NewForConfigOrDie(wecCfg)
    wdsDynamic := dynamic.NewForConfigOrDie(wdsCfg)
    itsDynamic := dynamic.NewForConfigOrDie(itsCfg)
    wecDynamic := dynamic.NewForConfigOrDie(wecCfg)
    
    return ksmetrics.NewLatencyCollector(
        wdsClient,
        wecClient,
        wdsDynamic,
        itsDynamic,
        wecDynamic,
        namespace,
        deployment,
        bindingName,
        itsNamespace,
    )
}
