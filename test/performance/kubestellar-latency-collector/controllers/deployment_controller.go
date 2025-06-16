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

package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
    "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const workStatusSuffixFmt = "appsv1-deployment-%s-%s"

// PerDeploymentCache holds all observed timestamps for one Deployment
type PerDeploymentCache struct {
    wdsDeploymentCreated      time.Time
    wdsDeploymentStatusTime   time.Time
    manifestWorkCreated       time.Time
    manifestWorkName          string
    appliedManifestWorkCreated time.Time
    wecDeploymentCreated      time.Time
    wecDeploymentStatusTime   time.Time
    workStatusTime            time.Time
}

// LatencyCollectorReconciler collects end-to-end latencies across all Deployments in a namespace
type LatencyCollectorReconciler struct {
    client.Client
    Scheme *runtime.Scheme

    // Clients for each cluster
    WdsClient  kubernetes.Interface
    WecClient  kubernetes.Interface
    WdsDynamic dynamic.Interface
    ItsDynamic dynamic.Interface
    WecDynamic dynamic.Interface

    // Configuration
    MonitoredNamespace string
	BindingName		   string
	bindingCreated time.Time

    // Cache mapping deployment name -> timestamps
    cache    map[string]*PerDeploymentCache
    cacheMux sync.Mutex

    // Histogram metrics for each stage
	totalPackagingHistogram   *prometheus.HistogramVec
	totalDeliveryHistogram    *prometheus.HistogramVec
	totalActivationHistogram  *prometheus.HistogramVec
	totalDownsyncHistogram    *prometheus.HistogramVec
	totalUpsyncReportHistogram *prometheus.HistogramVec
	totalUpsyncFinalHistogram  *prometheus.HistogramVec
	totalUpsyncHistogram       *prometheus.HistogramVec
	totalE2EHistogram         *prometheus.HistogramVec
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=control.kubestellar.io,resources=bindingpolicies;workstatuses,verbs=get;list
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks;appliedmanifestworks,verbs=get;list

func (r *LatencyCollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
    r.cache = make(map[string]*PerDeploymentCache)
    r.registerMetrics()
    return ctrl.NewControllerManagedBy(mgr).
        For(&appsv1.Deployment{}).
        Complete(r)
}

func (r *LatencyCollectorReconciler) registerMetrics() {
	// Initialize HistogramVecs for each lifecycle stage
	r.totalPackagingHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_packaging_duration_seconds",
		Help:    "Histogram of WDS deployment → ManifestWork creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalDeliveryHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_delivery_duration_seconds",
		Help:    "Histogram of ManifestWork → AppliedManifestWork creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalActivationHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_activation_duration_seconds",
		Help:    "Histogram of AppliedManifestWork → WEC deployment creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalDownsyncHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_downsync_duration_seconds",
		Help:    "Histogram of WDS deployment → WEC deployment creation durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalUpsyncReportHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_report_duration_seconds",
		Help:    "Histogram of WEC deployment → WorkStatus report durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalUpsyncFinalHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_finalization_duration_seconds",
		Help:    "Histogram of WorkStatus → WDS Deployment status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalUpsyncHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_upsync_duration_seconds",
		Help:    "Histogram of WEC deployment → WDS Deployment status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

	r.totalE2EHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubestellar_e2e_latency_duration_seconds",
		Help:    "Histogram of total binding → WDS status durations",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"deployment"})

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
	)
}

// fetchBindingPolicy retrieves the single BindingPolicy by name
// func (r *LatencyCollectorReconciler) fetchBindingPolicy(ctx context.Context) error {
//     gvr := schema.GroupVersionResource{Group: "control.kubestellar.io", Version: "v1alpha1", Resource: "bindingpolicies"}
//     b, err := r.WdsDynamic.Resource(gvr).Get(ctx, r.BindingName, metav1.GetOptions{})
//     if err != nil {
//         return fmt.Errorf("failed to get binding policy %s: %w", r.BindingName, err)
//     }
//     r.bindingCreated = b.GetCreationTimestamp().Time
//     log.FromContext(ctx).Info("🔖 BindingPolicy timestamp recorded", 
//         "bindingCreated", r.bindingCreated)
//     return nil
// }

func (r *LatencyCollectorReconciler) lookupManifestWork(ctx context.Context, deployName string, entry *PerDeploymentCache) {
    logger := log.FromContext(ctx).WithValues("deployment", deployName, "function", "lookupManifestWork")
    gvr := schema.GroupVersionResource{Group: "work.open-cluster-management.io", Version: "v1", Resource: "manifestworks"}
    list, err := r.ItsDynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
    if err != nil {
        logger.Error(err, "Failed to list ManifestWorks")
        return
    }
    
    logger.Info("Processing ManifestWorks", "count", len(list.Items))
    uniq := make(map[string]unstructured.Unstructured)
    for _, mw := range list.Items {
        uniq[mw.GetName()] = mw
    }
    
    found := false
    for name, mw := range uniq {
        manifestSlice, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
        for _, m := range manifestSlice {
            if mMap, ok := m.(map[string]interface{}); ok {
                kind, _, _ := unstructured.NestedString(mMap, "kind")
                metaName, _, _ := unstructured.NestedString(mMap, "metadata", "name")
                if kind == "Deployment" && metaName == deployName {
                    ts := mw.GetCreationTimestamp().Time
                    if entry.manifestWorkCreated.IsZero() {
                        entry.manifestWorkName = name
                        entry.manifestWorkCreated = ts
                        logger.Info("📦 ManifestWork creation timestamp recorded", 
                    		"manifestWork", name, "timestamp", ts)
                    } else {
                        logger.Info("📦 ManifestWork already recorded", 
                    		"manifestWork", name, "timestamp", ts)
                    }
                    found = true
                    break
                }
            }
        }
        if found {
            break
        }
    }
    
    if !found {
        logger.Info("No matching ManifestWork found for deployment")
    }
}

func (r *LatencyCollectorReconciler) lookupAppliedManifestWork(ctx context.Context, deployName string, entry *PerDeploymentCache) {
    logger := log.FromContext(ctx).WithValues("deployment", deployName, "function", "lookupAppliedManifestWork")
    if entry.manifestWorkName == "" {
        logger.Info("Skipping AppliedManifestWork lookup - ManifestWork name unknown")
        return
    }
    
    gvr := schema.GroupVersionResource{Group: "work.open-cluster-management.io", Version: "v1", Resource: "appliedmanifestworks"}
    list, err := r.WecDynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
    if err != nil {
        logger.Error(err, "Failed to list AppliedManifestWorks")
        return
    }
    
    logger.Info("Processing AppliedManifestWorks", "count", len(list.Items))
    found := false
    for _, aw := range list.Items {
        parts := strings.SplitN(aw.GetName(), "-", 2)
        if len(parts) == 2 && parts[1] == entry.manifestWorkName {
            ts := aw.GetCreationTimestamp().Time
            if entry.appliedManifestWorkCreated.IsZero() {
                entry.appliedManifestWorkCreated = ts
                logger.Info("📬 AppliedManifestWork creation timestamp recorded", 
                    "appliedManifestWork", aw.GetName(), "timestamp", ts)
            } else {
                logger.Info("📬 AppliedManifestWork already recorded", 
                    "appliedManifestWork", aw.GetName(), "timestamp", ts)
            }
            found = true
            break
        }
    }
    
    if !found {
        logger.Info("No matching AppliedManifestWork found", "manifestWork", entry.manifestWorkName)
    }
}

func (r *LatencyCollectorReconciler) lookupWorkStatus(ctx context.Context, deployName string, entry *PerDeploymentCache) {
    logger := log.FromContext(ctx).WithValues("deployment", deployName, "function", "lookupWorkStatus")
    gvr := schema.GroupVersionResource{Group: "control.kubestellar.io", Version: "v1alpha1", Resource: "workstatuses"}
    list, err := r.ItsDynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
    if err != nil {
        logger.Error(err, "Failed to list WorkStatuses")
        return
    }
    
    logger.Info("Processing WorkStatuses", "count", len(list.Items))
    suffix := fmt.Sprintf("appsv1-deployment-%s-%s", r.MonitoredNamespace, deployName)
    found := false
    for _, ws := range list.Items {
        if strings.HasSuffix(ws.GetName(), suffix) {
            ts := getStatusTime(&ws)
            if ts.IsZero() {
                logger.Info("WorkStatus found but no valid status timestamp", "workStatus", ws.GetName())
                continue
            }
            
            if entry.workStatusTime.IsZero() {
                entry.workStatusTime = ts
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

func (r *LatencyCollectorReconciler) lookupWECDeployment(ctx context.Context, deployName string, entry *PerDeploymentCache) {
    logger := log.FromContext(ctx).WithValues("deployment", deployName, "function", "lookupWECDeployment")
    dep, err := r.WecClient.AppsV1().Deployments(r.MonitoredNamespace).Get(ctx, deployName, metav1.GetOptions{})
    if err != nil {
        logger.Error(err, "Failed to get WEC Deployment")
        return
    }
    
    // Record creation timestamp
    if entry.wecDeploymentCreated.IsZero() {
        entry.wecDeploymentCreated = dep.CreationTimestamp.Time
        logger.Info("🏭 WEC Deployment creation timestamp recorded", 
            "timestamp", dep.CreationTimestamp.Time)
    }
    
    // Record status timestamp
    if st := getDeploymentStatusTime(dep); !st.IsZero() {
        if entry.wecDeploymentStatusTime.IsZero() || st.After(entry.wecDeploymentStatusTime) {
            entry.wecDeploymentStatusTime = st
            logger.Info("✅ WEC Deployment status timestamp recorded", 
                "timestamp", st)
        }
    } else {
        logger.Info("⚠️ No valid status conditions found for WEC Deployment")
    }
}

func (r *LatencyCollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.NamespacedName.Namespace != r.MonitoredNamespace {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithValues("deployment", req.Name, "function", "Reconcile")
    logger.Info("🔄 Reconcile called for Deployment", "namespace", req.NamespacedName.Namespace, "name", req.NamespacedName.Name)

	// fetch deployment etc (unchanged)
	var deploy appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deploy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// populate cache entry
	r.cacheMux.Lock()
	entry, exists := r.cache[deploy.Name]
	if !exists {
		entry = &PerDeploymentCache{}
		r.cache[deploy.Name] = entry
	}
	r.cacheMux.Unlock()

	// record WDS timestamps (unchanged)
	if entry.wdsDeploymentCreated.IsZero() {
		entry.wdsDeploymentCreated = deploy.CreationTimestamp.Time
	}
	if st := getDeploymentStatusTime(&deploy); !st.IsZero() {
		if entry.wdsDeploymentStatusTime.IsZero() || st.After(entry.wdsDeploymentStatusTime) {
			entry.wdsDeploymentStatusTime = st
		}
	}

	// other lookups (unchanged)
	r.lookupManifestWork(ctx, deploy.Name, entry)
	r.lookupAppliedManifestWork(ctx, deploy.Name, entry)
	r.lookupWorkStatus(ctx, deploy.Name, entry)
	r.lookupWECDeployment(ctx, deploy.Name, entry)

	// Observe each stage's duration into histograms
	now := time.Now()
	d := duration(entry.wdsDeploymentCreated, entry.manifestWorkCreated, now)
	r.totalPackagingHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.manifestWorkCreated, entry.appliedManifestWorkCreated, now)
	r.totalDeliveryHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.appliedManifestWorkCreated, entry.wecDeploymentCreated, now)
	r.totalActivationHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.wdsDeploymentCreated, entry.wecDeploymentCreated, now)
	r.totalDownsyncHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.wecDeploymentCreated, entry.workStatusTime, now)
	r.totalUpsyncReportHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.workStatusTime, entry.wdsDeploymentStatusTime, now)
	r.totalUpsyncFinalHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.wecDeploymentCreated, entry.wdsDeploymentStatusTime, now)
	r.totalUpsyncHistogram.WithLabelValues(deploy.Name).Observe(d)
	d = duration(entry.wdsDeploymentCreated, entry.wdsDeploymentStatusTime, now)
	r.totalE2EHistogram.WithLabelValues(deploy.Name).Observe(d)

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}


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

