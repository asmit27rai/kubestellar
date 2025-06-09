package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/metrics/legacyregistry"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	ksctlr "github.com/kubestellar/kubestellar/pkg/controller"
)

const (
	metricsPort        = ":2222"
	workStatusSuffixFmt = "appsv1-deployment-%s-%s"
)

func main() {
	var (
		enableLeaderElection bool
		itsName              string
		wdsName              string
		allowedGroupsString  string

		monitoredNamespace  string
		monitoredDeployment string
		bindingName         string

		// NEW: separate flags for each context
		wdsContext string
		itsContext string
		wecContext string

		kubeconfig string
	)

	// Define flags
	pflag.StringVar(&wdsContext, "wds-context", "", "Context name for WDS (Workload Description Space) cluster in kubeconfig")
	pflag.StringVar(&itsContext, "its-context", "", "Context name for ITS (Inventory & Transport Space) cluster in kubeconfig")
	pflag.StringVar(&wecContext, "wec-context", "", "Context name for WEC cluster in kubeconfig")

	pflag.StringVar(&bindingName, "binding-name", "", "Name of the binding policy for the monitored deployment")
	pflag.StringVar(&monitoredNamespace, "monitored-namespace", "default", "Namespace of the deployment to monitor")
	pflag.StringVar(&monitoredDeployment, "monitored-deployment", "", "Name of the deployment to monitor")
	pflag.StringVar(&itsName, "its-name", "", "Name of the Inventory and Transport Space to connect to")
	pflag.StringVar(&wdsName, "wds-name", "", "Name of the workload description space to connect to")
	pflag.StringVar(&allowedGroupsString, "api-groups", "", "List of allowed API groups, comma separated")
	pflag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager")
	pflag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")

	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	ctx, _ := ksctlr.InitialContext()
	logger := klog.FromContext(ctx)
	ctrl.SetLogger(logger)
	setupLog := logger.WithName("setup")

	// ─── 1) Build a loader for the kubeconfig file ─────────────────────────────────────────
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig // if empty, uses defaults (~/.kube/config)

	// We will override CurrentContext separately for each of WDS, ITS, and WEC:
	//   • wdsContext  → WDS cluster
	//   • itsContext  → ITS cluster
	//   • wecContext  → WEC cluster

	// ─── 2) Build WDS REST config ──────────────────────────────────────────────────────────
	wdsOverrides := &clientcmd.ConfigOverrides{CurrentContext: wdsContext}
	wdsClientConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, wdsOverrides,
	).ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to create WDS REST config (check --wds-context)")
		os.Exit(1)
	}
	wdsRestConfig := rest.CopyConfig(wdsClientConfig)

	// ─── 3) Build ITS REST config ──────────────────────────────────────────────────────────
	itsOverrides := &clientcmd.ConfigOverrides{CurrentContext: itsContext}
	itsClientConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, itsOverrides,
	).ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to create ITS REST config (check --its-context)")
		os.Exit(1)
	}
	itsRestConfig := rest.CopyConfig(itsClientConfig)

	// ─── 4) Build WEC REST config ──────────────────────────────────────────────────────────
	wecRestConfig := buildWECConfig(wecContext, loadingRules, &clientcmd.ConfigOverrides{}, setupLog)

	// ─── 5) Register the LatencyCollector if we have all required flags ────────────────────
	if monitoredDeployment != "" && bindingName != "" {
		latencyCollector := createLatencyCollector(
			wdsRestConfig,
			itsRestConfig,
			wecRestConfig,
			monitoredNamespace,
			monitoredDeployment,
			bindingName,
		)
		legacyregistry.MustRegister(latencyCollector)
	}

	// ─── 6) Start the Prometheus metrics server ─────────────────────────────────────────────
	setupLog.Info("Starting metrics server", "port", metricsPort)
	http.Handle("/metrics", legacyregistry.Handler())
	server := &http.Server{
		Addr:              metricsPort,
		ReadHeaderTimeout: 30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error(err, "Metrics server failed")
	}
}

func buildWECConfig(wecContext string, loadingRules *clientcmd.ClientConfigLoadingRules, 
	configOverrides *clientcmd.ConfigOverrides, logger klog.Logger) *rest.Config {
	
	if wecContext == "" {
		logger.Info("Using base config for WEC")
		config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules,
			configOverrides,
		).ClientConfig()
		if err != nil {
			logger.Error(err, "failed to create WEC config")
			return nil
		}
		return config
	}

	// Create config with specific context
	contextOverride := *configOverrides
	contextOverride.CurrentContext = wecContext

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&contextOverride,
	).ClientConfig()
	
	if err != nil {
		logger.Error(err, "failed to create WEC config with context", "context", wecContext)
		return nil
	}
	return config
}

func createLatencyCollector(
	wdsCfg, itsCfg, wecCfg *rest.Config,
	namespace, deployment, bindingName string,
) *LatencyCollector {
	wdsClient := kubernetes.NewForConfigOrDie(wdsCfg)
	wecClient := kubernetes.NewForConfigOrDie(wecCfg)
	wdsDynamic := dynamic.NewForConfigOrDie(wdsCfg)
	itsDynamic := dynamic.NewForConfigOrDie(itsCfg)
	wecDynamic := dynamic.NewForConfigOrDie(wecCfg)

	return NewLatencyCollector(
		wdsClient,
		wecClient,
		wdsDynamic,
		itsDynamic,
		wecDynamic,
		namespace,
		deployment,
		bindingName,
	)
}

// Updated LatencyCollector implementation
type LatencyCollector struct {
	wdsClient       kubernetes.Interface
	wecClient       kubernetes.Interface
	wdsDynamic      dynamic.Interface
	itsDynamic      dynamic.Interface
	wecDynamic      dynamic.Interface
	namespace       string
	monitoredDeploy string
	bindingName     string

	descriptors []*prometheus.Desc
}

func NewLatencyCollector(
	wdsClient kubernetes.Interface,
	wecClient kubernetes.Interface,
	wdsDynamic, itsDynamic, wecDynamic dynamic.Interface,
	namespace, monitoredDeploy, bindingName string,
) *LatencyCollector {
	labels := []string{"namespace", "deployment"}
	return &LatencyCollector{
		wdsClient:       wdsClient,
		wecClient:       wecClient,
		wdsDynamic:      wdsDynamic,
		itsDynamic:      itsDynamic,
		wecDynamic:      wecDynamic,
		namespace:       namespace,
		monitoredDeploy: monitoredDeploy,
		bindingName:     bindingName,
		descriptors: []*prometheus.Desc{
			prometheus.NewDesc(
				"kubestellar_downsync_binding_time_seconds",
				"Time from binding creation to WDS deployment creation",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_downsync_packaging_time_seconds",
				"Time from WDS deployment creation to ManifestWork creation",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_downsync_delivery_time_seconds",
				"Time from ManifestWork creation to AppliedManifestWork creation",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_downsync_activation_time_seconds",
				"Time from AppliedManifestWork creation to WEC deployment creation",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_total_downsync_time_seconds",
				"Total downsync time from WDS deployment creation to WEC deployment creation",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_upsync_report_time_seconds",
				"Time from WEC status update to WorkStatus update",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_upsync_finalization_time_seconds",
				"Time from WorkStatus update to WDS status update",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_total_upsync_time_seconds",
				"Total upsync time from WEC status update to WDS status update",
				labels, nil,
			),
			prometheus.NewDesc(
				"kubestellar_e2e_latency_seconds",
				"End-to-end latency from WDS creation to status update",
				labels, nil,
			),
		},
	}
}

func (lc *LatencyCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range lc.descriptors {
		ch <- desc
	}
}

func (lc *LatencyCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Get resource timestamps
	timestamps, err := lc.getResourceTimestamps(ctx)
	if err != nil {
		klog.Errorf("Failed to collect latency metrics: %v", err)
		return
	}

	// Calculate and expose metrics
	lc.exposeMetrics(ch, timestamps)
}

func (lc *LatencyCollector) getResourceTimestamps(ctx context.Context) (map[string]time.Time, error) {
	timestamps := make(map[string]time.Time)

	// Get binding creation time
	bindingGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "bindingpolicies",
	}
	binding, err := lc.wdsDynamic.Resource(bindingGVR).Get(ctx, lc.bindingName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get binding policy %s: %w", lc.bindingName, err)
	}
	timestamps["binding"] = binding.GetCreationTimestamp().Time
	fmt.Printf("Binding creation time: %s\n", timestamps["binding"])

	// Get WDS deployment
	wdsDeploy, err := lc.wdsClient.AppsV1().Deployments(lc.namespace).Get(ctx, lc.monitoredDeploy, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WDS deployment %s/%s: %w", lc.namespace, lc.monitoredDeploy, err)
	}
	timestamps["wdsDeploy"] = wdsDeploy.CreationTimestamp.Time
	timestamps["wdsStatus"] = getDeploymentStatusTime(wdsDeploy)

	fmt.Printf("WDS deployment creation time: %s\n", timestamps["wdsDeploy"])
	fmt.Printf("WDS deployment status time: %s\n", timestamps["wdsStatus"])	

	// Get ManifestWork
	manifestWorkGVR := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "manifestworks",
	}
	labelSelector := fmt.Sprintf("transport.kubestellar.io/originOwnerReferenceBindingKey=%s", lc.bindingName)
	manifestWorkList, err := lc.itsDynamic.Resource(manifestWorkGVR).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil || len(manifestWorkList.Items) == 0 {
		return nil, fmt.Errorf("failed to get ManifestWork for binding %s: %w", lc.bindingName, err)
	}
	// timestamps["manifestWork"] = manifestWorkList.Items[0].GetCreationTimestamp().Time

	// Get AppliedManifestWork
	appliedManifestWorkGVR := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "appliedmanifestworks",
	}
	// appliedManifestWorkList, err := lc.wecDynamic.Resource(appliedManifestWorkGVR).List(ctx, metav1.ListOptions{})
	// if err != nil || len(appliedManifestWorkList.Items) == 0 {
	// 	return nil, fmt.Errorf("failed to get AppliedManifestWork: %w", err)
	// }
	// var appliedManifestWork *unstructured.Unstructured
	// var manifest *unstructured.Unstructured
	// for _, item := range appliedManifestWorkList.Items {
	// 	for _, manifestWork := range manifestWorkList.Items {
	// 		if item.GetName() == manifestWork.GetName() {
	// 			spec, _, _ := unstructured.NestedStringMap(item.Object, "spec")
	// 			if spec["manifestWorkName"] == manifestWork.GetName() && spec["namespace"] == manifestWork.GetNamespace() {
	// 				appliedManifestWork = &item
	// 				manifest = &manifestWork
	// 				break
	// 			}
	// 		}
	// 	}
	// }
	// if manifest == nil {
	// 	return nil, fmt.Errorf("no ManifestWork found for AppliedManifestWork %s", appliedManifestWorkList.Items[0].GetName())
	// }
	// if appliedManifestWork == nil {
	// 	return nil, fmt.Errorf("no AppliedManifestWork found for ManifestWork %s in namespace %s", manifestWorkList.Items[0].GetName(), manifestWorkList.Items[0].GetNamespace())
	// }
	// timestamps["appliedManifestWork"] = appliedManifestWork.GetCreationTimestamp().Time

	var appliedManifestWork *unstructured.Unstructured

	for _, mw := range manifestWorkList.Items {
		amw, err := lc.wecDynamic.
			Resource(appliedManifestWorkGVR).
			Namespace(mw.GetNamespace()).         // look in the same namespace
			Get(ctx, mw.GetName(), metav1.GetOptions{})
		if err == nil {
			appliedManifestWork = amw
			// record timestamp and break
			timestamps["manifestWork"]       = mw.GetCreationTimestamp().Time
			timestamps["appliedManifestWork"] = amw.GetCreationTimestamp().Time
			break
		}
	}

	if appliedManifestWork == nil {
		return nil, fmt.Errorf(
			"no AppliedManifestWork found matching any ManifestWork for binding %q",
			lc.bindingName,
		)
	}

	// Get WEC deployment
	wecDeploy, err := lc.wecClient.AppsV1().Deployments(lc.namespace).Get(ctx, lc.monitoredDeploy, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WEC deployment %s/%s: %w", lc.namespace, lc.monitoredDeploy, err)
	}
	timestamps["wecDeploy"] = wecDeploy.CreationTimestamp.Time
	timestamps["wecStatus"] = getDeploymentStatusTime(wecDeploy)
	fmt.Printf("WEC deployment creation time: %s\n", timestamps["wecDeploy"])
	fmt.Printf("WEC deployment status time: %s\n", timestamps["wecStatus"])

	// Get WorkStatus
	workStatusGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "workstatuses",
	}
	workStatusSuffix := fmt.Sprintf(workStatusSuffixFmt, lc.namespace, lc.monitoredDeploy)
	workStatusList, err := lc.itsDynamic.Resource(workStatusGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list WorkStatuses: %w", err)
	}

	for _, ws := range workStatusList.Items {
		if strings.HasSuffix(ws.GetName(), workStatusSuffix) {
			timestamps["workStatus"] = getStatusTime(&ws)
			break
		}
	}

	fmt.Printf("Collected timestamps: %+v\n", timestamps)

	return timestamps, nil
}

func (lc *LatencyCollector) exposeMetrics(ch chan<- prometheus.Metric, timestamps map[string]time.Time) {
	// Helper function to expose metric if timestamp exists
	exposeMetric := func(desc *prometheus.Desc, start, end time.Time) {
		if !start.IsZero() && !end.IsZero() {
			duration := end.Sub(start).Seconds()
			ch <- prometheus.MustNewConstMetric(
				desc,
				prometheus.GaugeValue,
				duration,
				lc.namespace,
				lc.monitoredDeploy,
			)
		}
	}

	// Downsync metrics
	exposeMetric(lc.descriptors[0], timestamps["binding"], timestamps["wdsDeploy"])        // Binding time
	exposeMetric(lc.descriptors[1], timestamps["wdsDeploy"], timestamps["manifestWork"])   // Packaging time
	exposeMetric(lc.descriptors[2], timestamps["manifestWork"], timestamps["appliedManifestWork"]) // Delivery time
	exposeMetric(lc.descriptors[3], timestamps["appliedManifestWork"], timestamps["wecDeploy"])    // Activation time
	exposeMetric(lc.descriptors[4], timestamps["wdsDeploy"], timestamps["wecDeploy"])      // Total downsync

	// Upsync metrics
	if workStatusTime, exists := timestamps["workStatus"]; exists {
		exposeMetric(lc.descriptors[5], timestamps["wecStatus"], workStatusTime)            // Report time
		exposeMetric(lc.descriptors[6], workStatusTime, timestamps["wdsStatus"])            // Finalization time
		exposeMetric(lc.descriptors[7], timestamps["wecStatus"], timestamps["wdsStatus"])   // Total upsync
	}

	// E2E latency
	exposeMetric(lc.descriptors[8], timestamps["wdsDeploy"], timestamps["wdsStatus"])      // E2E latency
}

func getStatusTime(obj metav1.Object) time.Time {
	var latestTime time.Time
	for _, mf := range obj.GetManagedFields() {
		if mf.Operation == metav1.ManagedFieldsOperationUpdate && mf.Subresource == "status" {
			if mf.Time != nil && mf.Time.Time.After(latestTime) {
				latestTime = mf.Time.Time
			}
		}
	}
	return latestTime
}

func getDeploymentStatusTime(dep *appsv1.Deployment) time.Time {
    var latest time.Time
    for _, cond := range dep.Status.Conditions {
        // cond.LastUpdateTime is a metav1.Time; skip if zero
        if !cond.LastUpdateTime.IsZero() && cond.LastUpdateTime.Time.After(latest) {
            latest = cond.LastUpdateTime.Time
        }
    }
    return latest
}

// Implement StableCollector interface methods
func (lc *LatencyCollector) Create(version *semver.Version) bool { return true }
func (lc *LatencyCollector) ClearState()                        {}
func (lc *LatencyCollector) FQName() string                     { return "kubestellar_latency_metrics" }
func (lc *LatencyCollector) DeprecatedVersion() *semver.Version { return nil }
func (lc *LatencyCollector) IsDeprecated() bool                 { return false }
func (lc *LatencyCollector) IsHidden() bool                     { return false }
func (lc *LatencyCollector) IsCreated() bool                    { return true }