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

package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	workStatusSuffixFmt = "%s-%s-%s-%s" // Format: <apiVersion>-<kind>-<namespace>-<name>
)

// ClusterData holds timestamps for a specific cluster
type ClusterData struct {
	manifestWorkName           string
	manifestWorkCreated        time.Time
	appliedManifestWorkCreated time.Time
	wecObjectCreated           time.Time
	wecObjectStatusTime        time.Time
	workStatusTime             time.Time
}

// PerWorkloadCache holds all observed timestamps for one workload
type PerWorkloadCache struct {
	wdsObjectCreated    time.Time
	wdsObjectStatusTime time.Time
	clusterData         map[string]*ClusterData
	gvk                 schema.GroupVersionKind // Store GVK for the object
	name                string
	namespace           string
}

// LatencyCollectorReconciler collects end-to-end latencies across all workloads in a namespace
type LatencyCollectorReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Clients for each cluster
	WdsClient   kubernetes.Interface
	WecClients  map[string]kubernetes.Interface
	WdsDynamic  dynamic.Interface
	ItsDynamic  dynamic.Interface
	WecDynamics map[string]dynamic.Interface

	// Configuration
	MonitoredNamespace string
	BindingName        string
	bindingCreated     time.Time

	// Cache mapping workload key (namespace/name) -> timestamps
	cache    map[string]*PerWorkloadCache
	cacheMux sync.Mutex

	// Histogram metrics for each stage
	totalPackagingHistogram    *prometheus.HistogramVec
	totalDeliveryHistogram     *prometheus.HistogramVec
	totalActivationHistogram   *prometheus.HistogramVec
	totalDownsyncHistogram     *prometheus.HistogramVec
	totalUpsyncReportHistogram *prometheus.HistogramVec
	totalUpsyncFinalHistogram  *prometheus.HistogramVec
	totalUpsyncHistogram       *prometheus.HistogramVec
	totalE2EHistogram          *prometheus.HistogramVec
	workloadCountGauge         *prometheus.GaugeVec
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=control.kubestellar.io,resources=bindingpolicies;workstatuses,verbs=get;list
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks;appliedmanifestworks,verbs=get;list

func (r *LatencyCollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.cache = make(map[string]*PerWorkloadCache)
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Watches(
			&corev1.Pod{},
			&handler.EnqueueRequestForObject{},
		).
		Complete(r)
}

func (r *LatencyCollectorReconciler) RegisterMetrics() {
	// Initialize HistogramVecs for each lifecycle stage
	r.totalPackagingHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_packaging_duration_seconds",
		Help:    "Histogram of WDS object → ManifestWork creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalDeliveryHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_delivery_duration_seconds",
		Help:    "Histogram of ManifestWork → AppliedManifestWork creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalActivationHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_activation_duration_seconds",
		Help:    "Histogram of AppliedManifestWork → WEC object creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalDownsyncHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_duration_seconds",
		Help:    "Histogram of WDS object → WEC object creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalUpsyncReportHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_report_duration_seconds",
		Help:    "Histogram of WEC object → WorkStatus report durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalUpsyncFinalHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_finalization_duration_seconds",
		Help:    "Histogram of WorkStatus → WDS object status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalUpsyncHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_duration_seconds",
		Help:    "Histogram of WEC object → WDS object status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.totalE2EHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_e2e_latency_duration_seconds",
		Help:    "Histogram of total binding → WDS status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"workload", "cluster", "kind"})

	r.workloadCountGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_workload_count",
		Help: "Number of workload objects deployed in clusters",
	}, []string{"cluster", "kind"})

	// Register all histograms
	metrics.Registry.MustRegister(
		r.totalPackagingHistogram,
		r.totalDeliveryHistogram,
		r.totalActivationHistogram,
		r.totalDownsyncHistogram,
		r.totalUpsyncReportHistogram,
		r.totalUpsyncFinalHistogram,
		r.totalUpsyncHistogram,
		r.totalE2EHistogram,
		r.workloadCountGauge,
	)
}

func (r *LatencyCollectorReconciler) lookupManifestWorkForCluster(ctx context.Context, key string, clusterName string, entry *PerWorkloadCache) {
	logger := log.FromContext(ctx).WithValues(
		"workload", key,
		"cluster", clusterName,
		"function", "lookupManifestWorkForCluster",
	)

	clusterData := entry.clusterData[clusterName]
	if clusterData == nil {
		clusterData = &ClusterData{}
		entry.clusterData[clusterName] = clusterData
	}

	gvr := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "manifestworks",
	}

	// List ManifestWorks in the cluster namespace
	list, err := r.ItsDynamic.Resource(gvr).Namespace(clusterName).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to list ManifestWorks in cluster namespace")
		return
	}

	logger.Info("Processing ManifestWorks", "count", len(list.Items))
	for _, mw := range list.Items {
		manifestSlice, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
		for _, m := range manifestSlice {
			if mMap, ok := m.(map[string]interface{}); ok {
				kind, _, _ := unstructured.NestedString(mMap, "kind")
				metaName, _, _ := unstructured.NestedString(mMap, "metadata", "name")
				metaNamespace, _, _ := unstructured.NestedString(mMap, "metadata", "namespace")

				// Match both Pods and Deployments
				if (kind == "Deployment" || kind == "Pod") &&
					metaName == entry.name &&
					metaNamespace == entry.namespace {

					ts := mw.GetCreationTimestamp().Time
					if clusterData.manifestWorkCreated.IsZero() {
						clusterData.manifestWorkName = mw.GetName()
						clusterData.manifestWorkCreated = ts
						logger.Info("📦 ManifestWork creation timestamp recorded",
							"manifestWork", mw.GetName(), "timestamp", ts, "kind", kind)
					} else {
						logger.Info("📦 ManifestWork already recorded",
							"manifestWork", mw.GetName(), "timestamp", ts, "kind", kind)
					}
					return
				}
			}
		}
	}

	logger.Info("No matching ManifestWork found for workload in cluster namespace", "kind", entry.gvk.Kind)
}

func (r *LatencyCollectorReconciler) lookupAppliedManifestWork(ctx context.Context, key string, clusterName string, entry *PerWorkloadCache) {
	logger := log.FromContext(ctx).WithValues(
		"workload", key,
		"cluster", clusterName,
		"function", "lookupAppliedManifestWork",
	)

	clusterData := entry.clusterData[clusterName]
	if clusterData == nil || clusterData.manifestWorkName == "" {
		logger.Info("Skipping AppliedManifestWork lookup - ManifestWork name unknown")
		return
	}

	dynClient, exists := r.WecDynamics[clusterName]
	if !exists {
		logger.Error(nil, "WEC dynamic client not found for cluster")
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "appliedmanifestworks",
	}

	list, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to list AppliedManifestWorks")
		return
	}

	logger.Info("Processing AppliedManifestWorks", "count", len(list.Items))
	for _, aw := range list.Items {
		parts := strings.SplitN(aw.GetName(), "-", 2)
		if len(parts) == 2 && parts[1] == clusterData.manifestWorkName {
			ts := aw.GetCreationTimestamp().Time
			if clusterData.appliedManifestWorkCreated.IsZero() {
				clusterData.appliedManifestWorkCreated = ts
				logger.Info("📬 AppliedManifestWork creation timestamp recorded",
					"appliedManifestWork", aw.GetName(), "timestamp", ts)
			} else {
				logger.Info("📬 AppliedManifestWork already recorded",
					"appliedManifestWork", aw.GetName(), "timestamp", ts)
			}
			return
		}
	}

	logger.Info("No matching AppliedManifestWork found",
		"manifestWork", clusterData.manifestWorkName)
}

func normalizedGroupVersion(gvk schema.GroupVersionKind) string {
	if gvk.Group == "apps" && gvk.Version == "v1" {
		return "appsv1"
	}
	return strings.ToLower(gvk.GroupVersion().String())
}

func (r *LatencyCollectorReconciler) lookupWorkStatus(ctx context.Context, key string, clusterName string, entry *PerWorkloadCache) {
	logger := log.FromContext(ctx).WithValues(
		"workload", key,
		"cluster", clusterName,
		"function", "lookupWorkStatus",
	)

	clusterData := entry.clusterData[clusterName]
	if clusterData == nil {
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "workstatuses",
	}

	list, err := r.ItsDynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to list WorkStatuses")
		return
	}

	logger.Info("Processing WorkStatuses", "count", len(list.Items))
	// Format: <apiVersion>-<kind>-<namespace>-<name>
	suffix := fmt.Sprintf(workStatusSuffixFmt,
		normalizedGroupVersion(entry.gvk),
		strings.ToLower(entry.gvk.Kind),
		entry.namespace,
		entry.name)

	found := false
	for _, ws := range list.Items {
		if strings.HasSuffix(ws.GetName(), suffix) {
			ts := getStatusTime(&ws)
			if ts.IsZero() {
				logger.Info("WorkStatus found but no valid status timestamp", "workStatus", ws.GetName())
				continue
			}

			if clusterData.workStatusTime.IsZero() {
				clusterData.workStatusTime = ts
				logger.Info("📝 WorkStatus timestamp recorded",
					"workStatus", ws.GetName(), "timestamp", ts)
			} else {
				logger.Info("📝 WorkStatus already recorded",
					"workStatus", ws.GetName(), "timestamp", ts)
			}
			found = true
			break
		}
	}

	if !found {
		logger.Info("No matching WorkStatus found", "suffix", suffix)
	}
}

func (r *LatencyCollectorReconciler) lookupWECObject(ctx context.Context, key string, clusterName string, entry *PerWorkloadCache) {
	logger := log.FromContext(ctx).WithValues(
		"workload", key,
		"cluster", clusterName,
		"function", "lookupWECObject",
	)

	client, exists := r.WecClients[clusterName]
	if !exists {
		logger.Error(nil, "WEC client not found for cluster")
		return
	}

	var (
		createdTime time.Time
		statusTime  time.Time
	)

	switch entry.gvk.Kind {
	case "Deployment":
		dep, err := client.AppsV1().Deployments(entry.namespace).Get(ctx, entry.name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("WEC Deployment not yet created")
			} else {
				logger.Error(err, "Error fetching WEC Deployment")
			}
			return
		}
		createdTime = dep.CreationTimestamp.Time
		statusTime = getDeploymentStatusTime(dep)

	case "Pod":
		pod, err := client.CoreV1().Pods(entry.namespace).Get(ctx, entry.name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("WEC Pod not yet created")
			} else {
				logger.Error(err, "Error fetching WEC Pod")
			}
			return
		}
		createdTime = pod.CreationTimestamp.Time
		statusTime = getPodStatusTime(pod)
	}

	clusterData := entry.clusterData[clusterName]
	if clusterData == nil {
		clusterData = &ClusterData{}
		entry.clusterData[clusterName] = clusterData
	}

	// Record creation timestamp
	if clusterData.wecObjectCreated.IsZero() {
		clusterData.wecObjectCreated = createdTime
		logger.Info("🏭 WEC object creation timestamp recorded",
			"kind", entry.gvk.Kind, "timestamp", createdTime)
	}

	// Record status timestamp
	if !statusTime.IsZero() {
		if clusterData.wecObjectStatusTime.IsZero() || statusTime.After(clusterData.wecObjectStatusTime) {
			clusterData.wecObjectStatusTime = statusTime
			logger.Info("✅ WEC object status timestamp recorded",
				"kind", entry.gvk.Kind, "timestamp", statusTime)
		}
	} else {
		logger.Info("⚠️ No valid status conditions found for WEC object", "kind", entry.gvk.Kind)
	}
}

func (r *LatencyCollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.NamespacedName.Namespace != r.MonitoredNamespace {
		return ctrl.Result{}, nil
	}

	// Process Deployments and Pods independently
	var deploy appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deploy); err == nil {
		r.processDeployment(ctx, &deploy)
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err == nil {
		r.processPod(ctx, &pod)
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *LatencyCollectorReconciler) processDeployment(ctx context.Context, deploy *appsv1.Deployment) {
	logger := log.FromContext(ctx).WithValues("deployment", deploy.Name)
	logger.Info("Processing Deployment", "namespace", deploy.Namespace, "name", deploy.Name)
	key := fmt.Sprintf("deploy-%s/%s", deploy.Namespace, deploy.Name)

	r.cacheMux.Lock()
	defer r.cacheMux.Unlock()

	entry, exists := r.cache[key]
	if !exists {
		entry = &PerWorkloadCache{
			gvk:         appsv1.SchemeGroupVersion.WithKind("Deployment"),
			name:        deploy.Name,
			namespace:   deploy.Namespace,
			clusterData: make(map[string]*ClusterData),
		}
		r.cache[key] = entry
	}

	entry.wdsObjectCreated = deploy.CreationTimestamp.Time
	if st := getDeploymentStatusTime(deploy); !st.IsZero() {
		entry.wdsObjectStatusTime = st
	}

	r.processClusters(ctx, entry)
}

func (r *LatencyCollectorReconciler) processPod(ctx context.Context, pod *corev1.Pod) {
	logger := log.FromContext(ctx).WithValues("pod", pod.Name)
	logger.Info("Processing Pod", "namespace", pod.Namespace, "name", pod.Name)
	key := fmt.Sprintf("pod-%s/%s", pod.Namespace, pod.Name)

	r.cacheMux.Lock()
	defer r.cacheMux.Unlock()

	entry, exists := r.cache[key]
	if !exists {
		entry = &PerWorkloadCache{
			gvk:         corev1.SchemeGroupVersion.WithKind("Pod"),
			name:        pod.Name,
			namespace:   pod.Namespace,
			clusterData: make(map[string]*ClusterData),
		}
		r.cache[key] = entry
	}

	entry.wdsObjectCreated = pod.CreationTimestamp.Time
	if st := getPodStatusTime(pod); !st.IsZero() {
		entry.wdsObjectStatusTime = st
	}

	r.processClusters(ctx, entry)
}

func (r *LatencyCollectorReconciler) processClusters(ctx context.Context, entry *PerWorkloadCache) {
	logger := log.FromContext(ctx)
	logger.Info("Processing clusters for workload", "name", entry.name, "kind", entry.gvk.Kind)
	kind := entry.gvk.Kind

	// Construct the key as in processDeployment/processPod
	var key string
	switch kind {
	case "Deployment":
		key = fmt.Sprintf("deploy-%s/%s", entry.namespace, entry.name)
	case "Pod":
		key = fmt.Sprintf("pod-%s/%s", entry.namespace, entry.name)
	default:
		key = fmt.Sprintf("%s-%s/%s", strings.ToLower(kind), entry.namespace, entry.name)
	}

	for clusterName := range r.WecClients {
		if entry.clusterData[clusterName] == nil {
			entry.clusterData[clusterName] = &ClusterData{}
		}
		clusterData := entry.clusterData[clusterName]

		r.lookupManifestWorkForCluster(ctx, key, clusterName, entry)
		if clusterData.manifestWorkName != "" {
			r.lookupAppliedManifestWork(ctx, key, clusterName, entry)
			r.lookupWorkStatus(ctx, key, clusterName, entry)
			r.lookupWECObject(ctx, key, clusterName, entry)
		}

		// Record metrics
		now := time.Now()
		labels := prometheus.Labels{
			"workload": entry.name,
			"cluster":  clusterName,
			"kind":     kind,
		}

		r.totalPackagingHistogram.With(labels).Observe(
			duration(entry.wdsObjectCreated, clusterData.manifestWorkCreated, now),
		)
		r.totalDeliveryHistogram.With(labels).Observe(
			duration(clusterData.manifestWorkCreated, clusterData.appliedManifestWorkCreated, now),
		)
		r.totalActivationHistogram.With(labels).Observe(
			duration(clusterData.appliedManifestWorkCreated, clusterData.wecObjectCreated, now),
		)
		r.totalDownsyncHistogram.With(labels).Observe(
			duration(entry.wdsObjectCreated, clusterData.wecObjectCreated, now),
		)
		r.totalUpsyncReportHistogram.With(labels).Observe(
			duration(clusterData.wecObjectStatusTime, clusterData.workStatusTime, now),
		)
		r.totalUpsyncFinalHistogram.With(labels).Observe(
			duration(clusterData.workStatusTime, entry.wdsObjectStatusTime, now),
		)
		r.totalUpsyncHistogram.With(labels).Observe(
			duration(clusterData.wecObjectStatusTime, entry.wdsObjectStatusTime, now),
		)
		r.totalE2EHistogram.With(labels).Observe(
			duration(entry.wdsObjectCreated, entry.wdsObjectStatusTime, now),
		)
	}

	// Update workload count
	for clusterName, client := range r.WecClients {
		switch kind {
		case "Deployment":
			if deps, err := client.AppsV1().Deployments(r.MonitoredNamespace).List(ctx, metav1.ListOptions{}); err == nil {
				r.workloadCountGauge.WithLabelValues(clusterName, "Deployment").Set(float64(len(deps.Items)))
			}
		case "Pod":
			if pods, err := client.CoreV1().Pods(r.MonitoredNamespace).List(ctx, metav1.ListOptions{}); err == nil {
				r.workloadCountGauge.WithLabelValues(clusterName, "Pod").Set(float64(len(pods.Items)))
			}
		}
	}
}

// Helper functions
func duration(start, end, now time.Time) float64 {
	if start.IsZero() {
		return 0
	}
	if end.IsZero() {
		return now.Sub(start).Seconds()
	}
	return end.Sub(start).Seconds()
}

func getDeploymentStatusTime(dep *appsv1.Deployment) time.Time {
	var latest time.Time
	for _, cond := range dep.Status.Conditions {
		if !cond.LastUpdateTime.IsZero() && cond.LastUpdateTime.Time.After(latest) {
			latest = cond.LastUpdateTime.Time
		}
	}
	return latest
}

func getPodStatusTime(pod *corev1.Pod) time.Time {
	var latest time.Time
	for _, cond := range pod.Status.Conditions {
		if !cond.LastTransitionTime.IsZero() && cond.LastTransitionTime.Time.After(latest) {
			latest = cond.LastTransitionTime.Time
		}
	}
	return latest
}

func getStatusTime(obj metav1.Object) time.Time {
	var latest time.Time
	for _, mf := range obj.GetManagedFields() {
		if mf.Operation == metav1.ManagedFieldsOperationUpdate && mf.Subresource == "status" {
			if mf.Time != nil && mf.Time.Time.After(latest) {
				latest = mf.Time.Time
			}
		}
	}
	return latest
}
