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

// LatencyCollectorReconciler reconciles a Deployment object
type LatencyCollectorReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Clients
	WdsClient  kubernetes.Interface
	WecClient  kubernetes.Interface
	WdsDynamic dynamic.Interface
	ItsDynamic dynamic.Interface
	WecDynamic dynamic.Interface

	// Configuration
	MonitoredNamespace  string
	MonitoredDeployment string
	BindingName         string

	// Cache
	cache     LatencyCache
	cacheLock sync.Mutex

	// Metrics
	downsyncBindingTime        *prometheus.GaugeVec
	downsyncPackagingTime      *prometheus.GaugeVec
	downsyncDeliveryTime       *prometheus.GaugeVec
	downsyncActivationTime     *prometheus.GaugeVec
	totalDownsyncTime          *prometheus.GaugeVec
	upsyncReportTime           *prometheus.GaugeVec
	upsyncFinalizationTime     *prometheus.GaugeVec
	totalUpsyncTime            *prometheus.GaugeVec
	e2eLatencyTime             *prometheus.GaugeVec
}

type LatencyCache struct {
	bindingCreated            time.Time
	wdsDeploymentCreated      time.Time
	wdsDeploymentStatusTime   time.Time
	manifestWorkCreated       time.Time
	manifestWorkName          string
	appliedManifestWorkCreated time.Time
	wecDeploymentCreated      time.Time
	wecDeploymentStatusTime   time.Time
	workStatusTime            time.Time
}

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=control.kubestellar.io,resources=bindingpolicies;workstatuses,verbs=get;list
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks;appliedmanifestworks,verbs=get;list

func (r *LatencyCollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.registerMetrics()
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(r)
}

func (r *LatencyCollectorReconciler) registerMetrics() {
	labels := []string{"namespace", "deployment"}
	
	r.downsyncBindingTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_downsync_binding_time_seconds",
		Help: "Time from binding creation to WDS deployment creation",
	}, labels)
	
	r.downsyncPackagingTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_downsync_packaging_time_seconds",
		Help: "Time from WDS deployment creation to ManifestWork creation",
	}, labels)
	
	r.downsyncDeliveryTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_downsync_delivery_time_seconds",
		Help: "Time from ManifestWork creation to AppliedManifestWork creation",
	}, labels)
	
	r.downsyncActivationTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_downsync_activation_time_seconds",
		Help: "Time from AppliedManifestWork creation to WEC deployment creation",
	}, labels)
	
	r.totalDownsyncTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_total_downsync_time_seconds",
		Help: "Total downsync time from WDS deployment creation to WEC deployment creation",
	}, labels)
	
	r.upsyncReportTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_upsync_report_time_seconds",
		Help: "Time from WEC status update to WorkStatus update",
	}, labels)
	
	r.upsyncFinalizationTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_upsync_finalization_time_seconds",
		Help: "Time from WorkStatus update to WDS status update",
	}, labels)
	
	r.totalUpsyncTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_total_upsync_time_seconds",
		Help: "Total upsync time from WEC status update to WDS status update",
	}, labels)
	
	r.e2eLatencyTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubestellar_e2e_latency_seconds",
		Help: "End-to-end latency from WDS creation to status update",
	}, labels)

	metrics.Registry.MustRegister(
		r.downsyncBindingTime,
		r.downsyncPackagingTime,
		r.downsyncDeliveryTime,
		r.downsyncActivationTime,
		r.totalDownsyncTime,
		r.upsyncReportTime,
		r.upsyncFinalizationTime,
		r.totalUpsyncTime,
		r.e2eLatencyTime,
	)
}

func (r *LatencyCollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	
	// Skip if not our monitored deployment
	if req.NamespacedName.Namespace != r.MonitoredNamespace || 
	   req.NamespacedName.Name != r.MonitoredDeployment {
		return ctrl.Result{}, nil
	}

	// Lock cache for thread safety
	r.cacheLock.Lock()
	defer r.cacheLock.Unlock()

	// Fetch WDS Deployment
	wdsDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, req.NamespacedName, wdsDeploy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Update cache
	r.updateCacheFromDeployment(wdsDeploy)
	
	// Fetch BindingPolicy
	if r.cache.bindingCreated.IsZero() {
		if err := r.fetchBindingPolicy(ctx); err != nil {
			logger.Error(err, "Failed to fetch BindingPolicy")
		}
	}

	// Fetch ManifestWork
	if r.cache.manifestWorkCreated.IsZero() {
		if err := r.fetchManifestWork(ctx); err != nil {
			logger.Error(err, "Failed to fetch ManifestWork")
		}
	}

	// Fetch AppliedManifestWork
	if !r.cache.manifestWorkCreated.IsZero() && r.cache.appliedManifestWorkCreated.IsZero() {
		if err := r.fetchAppliedManifestWork(ctx); err != nil {
			logger.Error(err, "Failed to fetch AppliedManifestWork")
		}
	}

	// Fetch WorkStatus
	if r.cache.workStatusTime.IsZero() {
		if err := r.fetchWorkStatus(ctx); err != nil {
			logger.Error(err, "Failed to fetch WorkStatus")
		}
	}

	// Fetch WEC Deployment
	if r.cache.wecDeploymentCreated.IsZero() || r.cache.wecDeploymentStatusTime.IsZero() {
		if err := r.fetchWECDeployment(ctx); err != nil {
			logger.Error(err, "Failed to fetch WEC Deployment")
		}
	}

	// Compute and expose metrics
	r.computeMetrics()

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *LatencyCollectorReconciler) updateCacheFromDeployment(wdsDeploy *appsv1.Deployment) {
	if r.cache.wdsDeploymentCreated.IsZero() {
		r.cache.wdsDeploymentCreated = wdsDeploy.CreationTimestamp.Time
		log.FromContext(context.TODO()).Info("🛠️  WDS Deployment timestamp",
			"wdsDeploymentCreated", r.cache.wdsDeploymentCreated,
		)
	}
	
	if statusTime := getDeploymentStatusTime(wdsDeploy); !statusTime.IsZero() {
		r.cache.wdsDeploymentStatusTime = statusTime
		log.FromContext(context.TODO()).Info("📈  WDS Deployment status timestamp",
			"wdsDeploymentStatusTime", r.cache.wdsDeploymentStatusTime,
		)
	}
}

func (r *LatencyCollectorReconciler) fetchBindingPolicy(ctx context.Context) error {
	bindingGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "bindingpolicies",
	}
	binding, err := r.WdsDynamic.Resource(bindingGVR).Get(ctx, r.BindingName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get binding policy %s: %w", r.BindingName, err)
	}
	
	r.cache.bindingCreated = binding.GetCreationTimestamp().Time
	log.FromContext(ctx).Info("🔖  BindingPolicy timestamp",
       	"bindingCreated", r.cache.bindingCreated,
   	)
	return nil
}

// func (r *LatencyCollectorReconciler) fetchManifestWork(ctx context.Context) error {
// 	manifestWorkGVR := schema.GroupVersionResource{
// 		Group:    "work.open-cluster-management.io",
// 		Version:  "v1",
// 		Resource: "manifestworks",
// 	}
	
// 	labelSelector := fmt.Sprintf("transport.kubestellar.io/originOwnerReferenceBindingKey=%s", r.BindingName)
// 	manifestWorkList, err := r.ItsDynamic.Resource(manifestWorkGVR).List(ctx, metav1.ListOptions{
// 		LabelSelector: labelSelector,
// 	})

// 	fmt.Printf("Found %d ManifestWorks\n", len(manifestWorkList.Items))
// 	fmt.Printf("Name of all ManifestWorks:\n")
// 	for _, mw := range manifestWorkList.Items {
// 		name := mw.GetName()
// 		fmt.Printf("- %s\n", name)
// 	}
	
// 	if err != nil || len(manifestWorkList.Items) == 0 {
// 		return fmt.Errorf("failed to get ManifestWork for binding %s: %w", r.BindingName, err)
// 	}
	
// 	// Find ManifestWork that matches our deployment
// 	for _, mw := range manifestWorkList.Items {
// 		manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
// 		for _, manifest := range manifests {
// 			if manifestMap, ok := manifest.(map[string]interface{}); ok {
// 				if kind, _, _ := unstructured.NestedString(manifestMap, "kind"); kind == "Deployment" {
// 					if name, _, _ := unstructured.NestedString(manifestMap, "metadata", "name"); name == r.MonitoredDeployment {
// 						r.cache.manifestWorkCreated = mw.GetCreationTimestamp().Time
// 						log.FromContext(ctx).Info("📦  ManifestWork timestamp",
// 							"manifestWorkCreated", r.cache.manifestWorkCreated,
// 						)
// 						return nil
// 					}
// 				}
// 			}
// 		}
// 	}
	
// 	return fmt.Errorf("no matching ManifestWork found for deployment %s", r.MonitoredDeployment)
// }

// func (r *LatencyCollectorReconciler) fetchAppliedManifestWork(ctx context.Context) error {
// 	appliedManifestWorkGVR := schema.GroupVersionResource{
// 		Group:    "work.open-cluster-management.io",
// 		Version:  "v1",
// 		Resource: "appliedmanifestworks",
// 	}
	
// 	// List all AppliedManifestWorks
// 	appliedWorkList, err := r.WecDynamic.Resource(appliedManifestWorkGVR).List(ctx, metav1.ListOptions{})
// 	if err != nil {
// 		return fmt.Errorf("failed to list AppliedManifestWorks: %w", err)
// 	}

// 	fmt.Printf("Found %d AppliedManifestWorks\n", len(appliedWorkList.Items))
// 	fmt.Printf("Name of all AppliedManifestWorks:\n")
// 	for _, aw := range appliedWorkList.Items {
// 		name := aw.GetName()
// 		fmt.Printf("- %s\n", name)
// 	}
	
// 	// Find AppliedManifestWork that matches our deployment
// 	for _, aw := range appliedWorkList.Items {
// 		namespace, _, _ := unstructured.NestedString(aw.Object, "namespace")
// 		name, _, _ := unstructured.NestedString(aw.Object, "name")
		
// 		if namespace == r.MonitoredNamespace && name == r.MonitoredDeployment {
// 			r.cache.appliedManifestWorkCreated = aw.GetCreationTimestamp().Time
// 			log.FromContext(ctx).Info("📄  AppliedManifestWork timestamp",
// 				"appliedManifestWorkCreated", r.cache.appliedManifestWorkCreated,
// 			)
// 			return nil
// 		}
// 	}
	
// 	return fmt.Errorf("no AppliedManifestWork found for deployment %s/%s", 
// 		r.MonitoredNamespace, r.MonitoredDeployment)
// }

func (r *LatencyCollectorReconciler) fetchManifestWork(ctx context.Context) error {
    manifestWorkGVR := schema.GroupVersionResource{
        Group:    "work.open-cluster-management.io",
        Version:  "v1",
        Resource: "manifestworks",
    }

    list, err := r.ItsDynamic.Resource(manifestWorkGVR).List(ctx, metav1.ListOptions{
        LabelSelector: fmt.Sprintf("transport.kubestellar.io/originOwnerReferenceBindingKey=%s", r.BindingName),
    })
    if err != nil {
        return fmt.Errorf("listing ManifestWorks for binding %q: %w", r.BindingName, err)
    }
    if len(list.Items) == 0 {
        return fmt.Errorf("no ManifestWorks found for binding %q", r.BindingName)
    }

    // Dedupe
    uniq := make(map[string]unstructured.Unstructured, len(list.Items))
    for _, mw := range list.Items {
        uniq[mw.GetName()] = mw
    }

    // Find the one that contains your Deployment
    for name, mw := range uniq {
        manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
        for _, m := range manifests {
            if mMap, ok := m.(map[string]interface{}); ok {
                if kind, _, _ := unstructured.NestedString(mMap, "kind"); kind == "Deployment" {
                    if metaName, _, _ := unstructured.NestedString(mMap, "metadata", "name"); metaName == r.MonitoredDeployment {
                        // **Store the name** for later correlation
                        r.cache.manifestWorkName = name
                        r.cache.manifestWorkCreated = mw.GetCreationTimestamp().Time
                        log.FromContext(ctx).Info("📦  ManifestWork selected",
                            "name", name, "timestamp", r.cache.manifestWorkCreated,
                        )
                        return nil
                    }
                }
            }
        }
    }

    return fmt.Errorf("no matching ManifestWork found for deployment %q", r.MonitoredDeployment)
}


func (r *LatencyCollectorReconciler) fetchAppliedManifestWork(ctx context.Context) error {
    appliedGVR := schema.GroupVersionResource{
        Group:    "work.open-cluster-management.io",
        Version:  "v1",
        Resource: "appliedmanifestworks",
    }

    list, err := r.WecDynamic.Resource(appliedGVR).List(ctx, metav1.ListOptions{})
    if err != nil {
        return fmt.Errorf("listing AppliedManifestWorks: %w", err)
    }
    if len(list.Items) == 0 {
        return fmt.Errorf("no AppliedManifestWorks found")
    }

    // Dedupe
    uniq := make(map[string]unstructured.Unstructured, len(list.Items))
    for _, aw := range list.Items {
        uniq[aw.GetName()] = aw
    }

    // Now correlate purely by name suffix:
    // fullName = "<prefix>-<manifestWorkName>"
    for fullName, aw := range uniq {
        parts := strings.SplitN(fullName, "-", 2)
        if len(parts) != 2 {
            // unexpected format – skip
            continue
        }
        suffix := parts[1]
        if suffix == r.cache.manifestWorkName {
            r.cache.appliedManifestWorkCreated = aw.GetCreationTimestamp().Time
            log.FromContext(ctx).Info("📄  AppliedManifestWork matched",
                "appliedName", fullName,
                "timestamp", r.cache.appliedManifestWorkCreated,
            )
            return nil
        }
    }

    return fmt.Errorf("no AppliedManifestWork found for manifestWork %q", r.cache.manifestWorkName)
}

func (r *LatencyCollectorReconciler) fetchWorkStatus(ctx context.Context) error {
	workStatusGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "workstatuses",
	}
	
	workStatusSuffix := fmt.Sprintf(workStatusSuffixFmt, r.MonitoredNamespace, r.MonitoredDeployment)
	workStatusList, err := r.ItsDynamic.Resource(workStatusGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list WorkStatuses: %w", err)
	}

	for _, ws := range workStatusList.Items {
		if strings.HasSuffix(ws.GetName(), workStatusSuffix) {
			r.cache.workStatusTime = getStatusTime(&ws)
			log.FromContext(ctx).Info("📊  WorkStatus timestamp",
				"workStatusTime", r.cache.workStatusTime,
			)
			return nil
		}
	}
	
	return fmt.Errorf("workstatus not found for suffix %s", workStatusSuffix)
}

func (r *LatencyCollectorReconciler) fetchWECDeployment(ctx context.Context) error {
	dep, err := r.WecClient.AppsV1().Deployments(r.MonitoredNamespace).
		Get(ctx, r.MonitoredDeployment, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if r.cache.wecDeploymentCreated.IsZero() {
		r.cache.wecDeploymentCreated = dep.CreationTimestamp.Time
		log.FromContext(ctx).Info("🚀  WEC Deployment timestamp",
			"wecDeploymentCreated", r.cache.wecDeploymentCreated,
		)
	}
	
	if statusTime := getDeploymentStatusTime(dep); !statusTime.IsZero() {
		r.cache.wecDeploymentStatusTime = statusTime
		log.FromContext(ctx).Info("📈  WEC Deployment status timestamp",
			"wecDeploymentStatusTime", r.cache.wecDeploymentStatusTime,
		)
	}
	return nil
}

func (r *LatencyCollectorReconciler) computeMetrics() {
    now := time.Now()
    // Helper: if start==zero → 0; else if end==zero → now–start; else → end–start
    compute := func(start, end time.Time) float64 {
        if start.IsZero() {
            return 0
        }
        if end.IsZero() {
            return now.Sub(start).Seconds()
        }
        return end.Sub(start).Seconds()
    }

    labels := []string{r.MonitoredNamespace, r.MonitoredDeployment}

    // Downsync
    r.downsyncBindingTime.WithLabelValues(labels...).Set(
        compute(r.cache.bindingCreated, r.cache.wdsDeploymentCreated),
    )
    r.downsyncPackagingTime.WithLabelValues(labels...).Set(
        compute(r.cache.wdsDeploymentCreated, r.cache.manifestWorkCreated),
    )
    r.downsyncDeliveryTime.WithLabelValues(labels...).Set(
        compute(r.cache.manifestWorkCreated, r.cache.appliedManifestWorkCreated),
    )
    r.downsyncActivationTime.WithLabelValues(labels...).Set(
        compute(r.cache.appliedManifestWorkCreated, r.cache.wecDeploymentCreated),
    )
    r.totalDownsyncTime.WithLabelValues(labels...).Set(
        compute(r.cache.bindingCreated, r.cache.wecDeploymentCreated),
    )

    // Upsync
    r.upsyncReportTime.WithLabelValues(labels...).Set(
        compute(r.cache.wecDeploymentCreated, r.cache.workStatusTime),
    )
    r.upsyncFinalizationTime.WithLabelValues(labels...).Set(
        compute(r.cache.workStatusTime, r.cache.wdsDeploymentStatusTime),
    )
    r.totalUpsyncTime.WithLabelValues(labels...).Set(
        compute(r.cache.wecDeploymentCreated, r.cache.wdsDeploymentStatusTime),
    )

    // E2E
    r.e2eLatencyTime.WithLabelValues(labels...).Set(
        compute(r.cache.bindingCreated, r.cache.wdsDeploymentStatusTime),
    )
}

func getDeploymentStatusTime(dep *appsv1.Deployment) time.Time {
	var latest time.Time
	for _, cond := range dep.Status.Conditions {
		// Use LastUpdateTime instead of LastTransitionTime
		if !cond.LastUpdateTime.IsZero() && cond.LastUpdateTime.Time.After(latest) {
			latest = cond.LastUpdateTime.Time
		}
	}
	return latest
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
