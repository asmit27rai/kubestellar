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

    // Aggregated metrics (no per-deployment labels)
    totalBindingTime       prometheus.Gauge
    totalPackagingTime     prometheus.Gauge
    totalDeliveryTime      prometheus.Gauge
    totalActivationTime    prometheus.Gauge
    totalDownsyncTime      prometheus.Gauge
    totalUpsyncReportTime  prometheus.Gauge
    totalUpsyncFinalTime   prometheus.Gauge
    totalUpsyncTime        prometheus.Gauge
    totalE2ELatencyTime    prometheus.Gauge
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
    r.totalBindingTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_downsync_binding_time_seconds",
        Help: "Sum of binding→WDS deployment creation times across all Deployments",
    })
    r.totalPackagingTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_downsync_packaging_time_seconds",
        Help: "Sum of WDS deployment→ManifestWork creation times across all Deployments",
    })
    r.totalDeliveryTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_downsync_delivery_time_seconds",
        Help: "Sum of ManifestWork→AppliedManifestWork creation times across all Deployments",
    })
    r.totalActivationTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_downsync_activation_time_seconds",
        Help: "Sum of AppliedManifestWork→WEC deployment creation times across all Deployments",
    })
    r.totalDownsyncTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_downsync_time_seconds",
        Help: "Sum of binding→WEC deployment creation times across all Deployments",
    })
    r.totalUpsyncReportTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_upsync_report_time_seconds",
        Help: "Sum of WEC deployment→WorkStatus report times across all Deployments",
    })
    r.totalUpsyncFinalTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_upsync_finalization_time_seconds",
        Help: "Sum of WorkStatus→WDS Deployment status times across all Deployments",
    })
    r.totalUpsyncTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_upsync_time_seconds",
        Help: "Sum of WEC deployment→WDS Deployment status times across all Deployments",
    })
    r.totalE2ELatencyTime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "kubestellar_total_e2e_latency_seconds",
        Help: "Sum of binding→WDS status times across all Deployments",
    })

    metrics.Registry.MustRegister(
        r.totalBindingTime,
        r.totalPackagingTime,
        r.totalDeliveryTime,
        r.totalActivationTime,
        r.totalDownsyncTime,
        r.totalUpsyncReportTime,
        r.totalUpsyncFinalTime,
        r.totalUpsyncTime,
        r.totalE2ELatencyTime,
    )
}

// fetchBindingPolicy retrieves the single BindingPolicy by name
func (r *LatencyCollectorReconciler) fetchBindingPolicy(ctx context.Context) error {
    gvr := schema.GroupVersionResource{Group: "control.kubestellar.io", Version: "v1alpha1", Resource: "bindingpolicies"}
    b, err := r.WdsDynamic.Resource(gvr).Get(ctx, r.BindingName, metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("failed to get binding policy %s: %w", r.BindingName, err)
    }
    r.bindingCreated = b.GetCreationTimestamp().Time
    log.FromContext(ctx).Info("🔖 BindingPolicy timestamp recorded", 
        "bindingCreated", r.bindingCreated)
    return nil
}

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

    // 0) fetch binding once
    if r.bindingCreated.IsZero() {
        if err := r.fetchBindingPolicy(ctx); err != nil {
            log.FromContext(ctx).Error(err, "fetching binding policy")
        }
    }

    // fetch the Deployment
    var deploy appsv1.Deployment
    if err := r.Get(ctx, req.NamespacedName, &deploy); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    r.cacheMux.Lock()
    entry, exists := r.cache[deploy.Name]
    if !exists {
        entry = &PerDeploymentCache{}
        r.cache[deploy.Name] = entry
    }
    r.cacheMux.Unlock()

    // 1) WDS Deployment timestamps
    if entry.wdsDeploymentCreated.IsZero() {
        entry.wdsDeploymentCreated = deploy.CreationTimestamp.Time
        logger.Info("🏢 WDS Deployment creation timestamp recorded",
            "timestamp", deploy.CreationTimestamp.Time)
    }
    if st := getDeploymentStatusTime(&deploy); !st.IsZero() {
        if entry.wdsDeploymentStatusTime.IsZero() || st.After(entry.wdsDeploymentStatusTime) {
            entry.wdsDeploymentStatusTime = st
            logger.Info("📊 WDS Deployment status timestamp recorded", 
                "timestamp", st)
        }
    }

    // 2) ManifestWork
    r.lookupManifestWork(ctx, deploy.Name, entry)

    // 3) AppliedManifestWork
    r.lookupAppliedManifestWork(ctx, deploy.Name, entry)

    // 4) WorkStatus
    r.lookupWorkStatus(ctx, deploy.Name, entry)

    // 5) WEC Deployment
    r.lookupWECDeployment(ctx, deploy.Name, entry)

    // Update aggregated totals (bindingCreated used for all entries)
    r.updateAggregates()

    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *LatencyCollectorReconciler) updateAggregates() {
    now := time.Now()
    var sb, sp, sd, sa, sdown, sur, sf, su, se float64
    for _, e := range r.cache {
        sb += duration(r.bindingCreated, e.wdsDeploymentCreated, now)
        sp += duration(e.wdsDeploymentCreated, e.manifestWorkCreated, now)
        sd += duration(e.manifestWorkCreated, e.appliedManifestWorkCreated, now)
        sa += duration(e.appliedManifestWorkCreated, e.wecDeploymentCreated, now)
        sdown += duration(r.bindingCreated, e.wecDeploymentCreated, now)
        sur += duration(e.wecDeploymentCreated, e.workStatusTime, now)
        sf += duration(e.workStatusTime, e.wdsDeploymentStatusTime, now)
        su += duration(e.wecDeploymentCreated, e.wdsDeploymentStatusTime, now)
        se += duration(r.bindingCreated, e.wdsDeploymentStatusTime, now)
    }

    r.totalBindingTime.Set(sb)
    r.totalPackagingTime.Set(sp)
    r.totalDeliveryTime.Set(sd)
    r.totalActivationTime.Set(sa)
    r.totalDownsyncTime.Set(sdown)
    r.totalUpsyncReportTime.Set(sur)
    r.totalUpsyncFinalTime.Set(sf)
    r.totalUpsyncTime.Set(su)
    r.totalE2ELatencyTime.Set(se)
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

