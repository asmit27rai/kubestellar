// /*
// Copyright 2024 The KubeStellar Authors.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// */

// package metrics

// import (
// 	"context"
// 	"fmt"
// 	"strings"
// 	"time"

// 	"github.com/blang/semver/v4"
// 	"github.com/prometheus/client_golang/prometheus"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//     "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
//     "k8s.io/apimachinery/pkg/runtime/schema"
//     "k8s.io/client-go/dynamic"
//     "k8s.io/client-go/kubernetes"
//     "k8s.io/klog/v2"
//     appsv1 "k8s.io/api/apps/v1"
//     corev1 "k8s.io/api/core/v1"
// )

// // LatencyCollector exposes KubeStellar latency metrics
// type LatencyCollector struct {
// 	wdsClient       kubernetes.Interface
// 	wecClient       kubernetes.Interface
// 	itsDynamic      dynamic.Interface
// 	wecDynamic      dynamic.Interface
// 	namespace       string
// 	monitoredDeploy string
// 	bindingName     string

// 	// Downsync metrics
// 	downsyncBindingTime    *prometheus.Desc
// 	downsyncPackagingTime  *prometheus.Desc
// 	downsyncDeliveryTime   *prometheus.Desc
// 	downsyncActivationTime *prometheus.Desc
// 	totalDownsyncTime      *prometheus.Desc

// 	// Upsync metrics
// 	upsyncReportTime       *prometheus.Desc
// 	upsyncFinalizationTime *prometheus.Desc
// 	totalUpsyncTime        *prometheus.Desc

// 	// E2E latency
// 	e2eLatency *prometheus.Desc
// }

// func NewLatencyCollector(
// 	wdsClient kubernetes.Interface,
// 	wecClient kubernetes.Interface,
// 	itsDynamic, wecDynamic dynamic.Interface,
// 	namespace, monitoredDeploy, bindingName string,
// ) *LatencyCollector {
// 	return &LatencyCollector{
// 		wdsClient:       wdsClient,
// 		wecClient:       wecClient,
// 		itsDynamic:      itsDynamic,
// 		wecDynamic:      wecDynamic,
// 		namespace:       namespace,
// 		monitoredDeploy: monitoredDeploy,
// 		bindingName:     bindingName,
		
// 		downsyncBindingTime: prometheus.NewDesc(
// 			"kubestellar_downsync_binding_time_seconds",
// 			"Time from binding creation to WDS deployment creation",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		downsyncPackagingTime: prometheus.NewDesc(
// 			"kubestellar_downsync_packaging_time_seconds",
// 			"Time from WDS deployment creation to ManifestWork creation",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		downsyncDeliveryTime: prometheus.NewDesc(
// 			"kubestellar_downsync_delivery_time_seconds",
// 			"Time from ManifestWork creation to AppliedManifestWork creation",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		downsyncActivationTime: prometheus.NewDesc(
// 			"kubestellar_downsync_activation_time_seconds",
// 			"Time from AppliedManifestWork creation to WEC deployment creation",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		totalDownsyncTime: prometheus.NewDesc(
// 			"kubestellar_total_downsync_time_seconds",
// 			"Total downsync time from WDS deployment creation to WEC deployment creation",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		upsyncReportTime: prometheus.NewDesc(
// 			"kubestellar_upsync_report_time_seconds",
// 			"Time from WEC status update to WorkStatus update",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		upsyncFinalizationTime: prometheus.NewDesc(
// 			"kubestellar_upsync_finalization_time_seconds",
// 			"Time from WorkStatus update to WDS status update",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		totalUpsyncTime: prometheus.NewDesc(
// 			"kubestellar_total_upsync_time_seconds",
// 			"Total upsync time from WEC status update to WDS status update",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 		e2eLatency: prometheus.NewDesc(
// 			"kubestellar_e2e_latency_seconds",
// 			"End-to-end latency from WDS creation to status update",
// 			[]string{"namespace", "deployment"}, nil,
// 		),
// 	}
// }

// func (lc *LatencyCollector) Describe(ch chan<- *prometheus.Desc) {
// 	ch <- lc.downsyncBindingTime
// 	ch <- lc.downsyncPackagingTime
// 	ch <- lc.downsyncDeliveryTime
// 	ch <- lc.downsyncActivationTime
// 	ch <- lc.totalDownsyncTime
// 	ch <- lc.upsyncReportTime
// 	ch <- lc.upsyncFinalizationTime
// 	ch <- lc.totalUpsyncTime
// 	ch <- lc.e2eLatency
// }

// func (lc *LatencyCollector) Collect(ch chan<- prometheus.Metric) {
// 	latencies, err := lc.calculateLatencies()
// 	if err != nil {
// 		klog.Errorf("Failed to calculate latencies: %v", err)
// 		return
// 	}

// 	// Add labels for namespace and deployment
// 	labels := []string{lc.namespace, lc.monitoredDeploy}

// 	ch <- prometheus.MustNewConstMetric(
// 		lc.downsyncBindingTime,
// 		prometheus.GaugeValue,
// 		latencies.BindingTime.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.downsyncPackagingTime,
// 		prometheus.GaugeValue,
// 		latencies.PackagingTime.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.downsyncDeliveryTime,
// 		prometheus.GaugeValue,
// 		latencies.DeliveryTime.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.downsyncActivationTime,
// 		prometheus.GaugeValue,
// 		latencies.ActivationTime.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.totalDownsyncTime,
// 		prometheus.GaugeValue,
// 		latencies.TotalDownsync.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.upsyncReportTime,
// 		prometheus.GaugeValue,
// 		latencies.ReportTime.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.upsyncFinalizationTime,
// 		prometheus.GaugeValue,
// 		latencies.Finalization.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.totalUpsyncTime,
// 		prometheus.GaugeValue,
// 		latencies.TotalUpsync.Seconds(),
// 		labels...,
// 	)
// 	ch <- prometheus.MustNewConstMetric(
// 		lc.e2eLatency,
// 		prometheus.GaugeValue,
// 		latencies.E2ELatency.Seconds(),
// 		labels...,
// 	)
// }

// func (lc *LatencyCollector) calculateLatencies() (*LatencyData, error) {
//     data := &LatencyData{}
//     klog.Infof("Starting latency calculation for %s/%s", lc.namespace, lc.monitoredDeploy)
    
//     // 1. Get binding creation time
//     bindingGVR := schema.GroupVersionResource{
// 		Group:    "control.kubestellar.io",
// 		Version:  "v1alpha1",
// 		Resource: "bindingpolicies",
// 	}

// 	bindingObj, err := lc.itsDynamic.Resource(bindingGVR).Get(
// 		context.TODO(), lc.bindingName, metav1.GetOptions{},
// 	)
// 	if err != nil {
// 		klog.Errorf("❌ Error getting binding policy: %v", err)
// 		return nil, fmt.Errorf("failed to get binding policy: %w", err)
// 	}
// 	data.BindingCreate = bindingObj.GetCreationTimestamp().Time
// 	klog.Infof("🔗 Binding policy created: %s", data.BindingCreate.Format(time.RFC3339Nano))

//     // 2. Get WDS deployment
//     wdsDep, err := lc.wdsClient.AppsV1().Deployments(lc.namespace).Get(
//         context.TODO(), lc.monitoredDeploy, metav1.GetOptions{},
//     )
//     if err != nil {
//         klog.Errorf("❌ Error getting WDS deployment: %v", err)
//         return nil, err
//     }
//     data.WDSDeployCreate = wdsDep.CreationTimestamp.Time
//     data.WDSDeployStatus = getDeploymentStatusTime(wdsDep)
//     klog.Infof("📦 WDS deployment - Created: %s, Status Updated: %s", 
//         data.WDSDeployCreate.Format(time.RFC3339Nano),
//         data.WDSDeployStatus.Format(time.RFC3339Nano))

//     // 3. Find ManifestWorks
//     manifestGVR := schema.GroupVersionResource{
//         Group:    "work.open-cluster-management.io",
//         Version:  "v1",
//         Resource: "manifestworks",
//     }
    
//     manifestList, err := lc.itsDynamic.Resource(manifestGVR).List(
//         context.TODO(), metav1.ListOptions{},
//     )
//     if err != nil {
//         klog.Errorf("❌ Error listing ManifestWorks: %v", err)
//     } else {
//         klog.Infof("🔍 Found %d ManifestWorks", len(manifestList.Items))
//         for _, mw := range manifestList.Items {
//             if containsDeployment(&mw, lc.monitoredDeploy, lc.namespace) {
//                 data.ManifestWorkCreate = mw.GetCreationTimestamp().Time
//                 klog.Infof("✅ Found relevant ManifestWork %s: %s", 
//                     mw.GetName(), data.ManifestWorkCreate.Format(time.RFC3339Nano))
//                 break
//             }
//         }
//     }

//     // 4. Find AppliedManifestWorks
//     appliedGVR := schema.GroupVersionResource{
//         Group:    "work.open-cluster-management.io",
//         Version:  "v1",
//         Resource: "appliedmanifestworks",
//     }
    
//     appliedList, err := lc.wecDynamic.Resource(appliedGVR).List(
//         context.TODO(), metav1.ListOptions{},
//     )
//     if err != nil {
//         klog.Errorf("❌ Error listing AppliedManifestWorks: %v", err)
//     } else {
//         klog.Infof("🔍 Found %d AppliedManifestWorks", len(appliedList.Items))
//         for _, amw := range appliedList.Items {
//             if containsDeployment(&amw, lc.monitoredDeploy, lc.namespace) {
//                 data.AppliedManifestCreate = amw.GetCreationTimestamp().Time
//                 klog.Infof("✅ Found relevant AppliedManifestWork %s: %s", 
//                     amw.GetName(), data.AppliedManifestCreate.Format(time.RFC3339Nano))
//                 break
//             }
//         }
//     }

//     // 5. Get WEC deployment
//     wecDep, err := lc.wecClient.AppsV1().Deployments(lc.namespace).Get(
//         context.TODO(), lc.monitoredDeploy, metav1.GetOptions{},
//     )
//     if err != nil {
//         klog.Errorf("❌ Error getting WEC deployment: %v", err)
//     } else {
//         data.WECDeployCreate = wecDep.CreationTimestamp.Time
//         data.WECDeployStatus = getDeploymentStatusTime(wecDep)
//         klog.Infof("📦 WEC deployment - Created: %s, Status Updated: %s", 
//             data.WECDeployCreate.Format(time.RFC3339Nano),
//             data.WECDeployStatus.Format(time.RFC3339Nano))
//     }

//     // 6. Find WorkStatus
//     statusGVR := schema.GroupVersionResource{
//         Group:    "control.kubestellar.io",
//         Version:  "v1alpha1",
//         Resource: "workstatuses", // Corrected resource name
//     }
    
//     // Get all WorkStatuses in the namespace
//     statusList, err := lc.itsDynamic.Resource(statusGVR).Namespace(lc.namespace).List(
//         context.TODO(), metav1.ListOptions{},
//     )
//     if err != nil {
//         klog.Errorf("❌ Error listing WorkStatuses: %v", err)
//     } else {
//         klog.Infof("🔍 Found %d WorkStatuses", len(statusList.Items))
        
//         // Find the one for our deployment
//         for _, ws := range statusList.Items {
//             targetObj, _, _ := unstructured.NestedString(ws.Object, "spec", "workload", "manifests", "metadata", "name")
//             if targetObj == lc.monitoredDeploy {
//                 data.WorkStatusUpdate = getStatusTimeFromManagedFields(ws.GetManagedFields())
//                 if data.WorkStatusUpdate.IsZero() {
//                     data.WorkStatusUpdate = ws.GetCreationTimestamp().Time
//                     klog.Info("    ⏱️ Using creation time as WorkStatus update time")
//                 } else {
//                     klog.Infof("    ⏱️ WorkStatus update time: %s", data.WorkStatusUpdate.Format(time.RFC3339Nano))
//                 }
//                 klog.Infof("✅ Found relevant WorkStatus: %s", ws.GetName())
//                 break
//             }
//         }
//     }

//     // Calculate latencies with detailed logging
//     klog.Info("📊 Calculating latencies:")
//     data.BindingTime = safeDuration(data.WDSDeployCreate, data.BindingCreate, "BindingTime")
//     data.PackagingTime = safeDuration(data.ManifestWorkCreate, data.WDSDeployCreate, "PackagingTime")
//     data.DeliveryTime = safeDuration(data.AppliedManifestCreate, data.ManifestWorkCreate, "DeliveryTime")
//     data.ActivationTime = safeDuration(data.WECDeployCreate, data.AppliedManifestCreate, "ActivationTime")
//     data.TotalDownsync = safeDuration(data.WECDeployCreate, data.WDSDeployCreate, "TotalDownsync")

//     // Handle zero timestamps for upsync metrics
//     if !data.WorkStatusUpdate.IsZero() && !data.WECDeployStatus.IsZero() {
//         data.ReportTime = safeDuration(data.WorkStatusUpdate, data.WECDeployStatus, "ReportTime")
//     }
//     if !data.WorkStatusUpdate.IsZero() && !data.WDSDeployStatus.IsZero() {
//         data.Finalization = safeDuration(data.WDSDeployStatus, data.WorkStatusUpdate, "Finalization")
//     }
//     if !data.WECDeployStatus.IsZero() && !data.WDSDeployStatus.IsZero() {
//         data.TotalUpsync = safeDuration(data.WDSDeployStatus, data.WECDeployStatus, "TotalUpsync")
//     }

//     data.E2ELatency = safeDuration(data.WDSDeployStatus, data.WDSDeployCreate, "E2ELatency")

//     klog.Info("✅ Latency calculation completed")
//     return data, nil
// }

// func safeDuration(later, earlier time.Time, name string) time.Duration {
//     if later.IsZero() || earlier.IsZero() {
//         klog.Warningf("⚠️ Zero time in %s: later=%v, earlier=%v", name, later, earlier)
//         return 0
//     }
//     if later.Before(earlier) {
//         klog.Warningf("⚠️ Invalid time order in %s: later=%s is before earlier=%s", 
//             name, later.Format(time.RFC3339Nano), earlier.Format(time.RFC3339Nano))
//         return 0
//     }
//     duration := later.Sub(earlier)
//     klog.Infof("  %s: %s", name, duration)
//     return duration
// }

// func getString(obj map[string]interface{}, fields ...string) string {
//     val, found, err := unstructured.NestedString(obj, fields...)
//     if !found || err != nil {
//         return ""
//     }
//     return val
// }

// func calculateDuration(later, earlier time.Time, name string) time.Duration {
//     if later.IsZero() || earlier.IsZero() {
//         klog.Warningf("Zero time in %s: later=%v, earlier=%v", name, later, earlier)
//         return 0
//     }
//     if later.Before(earlier) {
//         klog.Warningf("Invalid time order in %s: later=%s is before earlier=%s", 
//             name, later.Format(time.RFC3339Nano), earlier.Format(time.RFC3339Nano))
//         return 0
//     }
//     return later.Sub(earlier)
// }

// func getStatusTimeFromManagedFields(managedFields []metav1.ManagedFieldsEntry) time.Time {
//     var latest time.Time
//     for _, mf := range managedFields {
//         if mf.Operation == "Update" && mf.Subresource == "status" {
//             if mf.Time != nil && mf.Time.After(latest) {
//                 latest = mf.Time.Time
//             }
//         }
//     }
//     return latest
// }

// // getStatusTime extracts the last status update time from ObjectMeta's ManagedFields.
// func getStatusTime(meta metav1.ObjectMeta) time.Time {
//     if t := getStatusTimeFromManagedFields(meta.ManagedFields); !t.IsZero() {
//         return t
//     }
    
//     // Parse status from annotations as fallback
//     if tStr, ok := meta.Annotations["kubestellar.io/status-time"]; ok {
//         if t, err := time.Parse(time.RFC3339Nano, tStr); err == nil {
//             return t
//         }
//     }
//     return meta.CreationTimestamp.Time
// }

// func getDeploymentStatusTime(dep *appsv1.Deployment) time.Time {
//     // First try managed fields
//     if t := getStatusTimeFromManagedFields(dep.ObjectMeta.ManagedFields); !t.IsZero() {
//         return t
//     }
    
//     // Then try conditions
//     for _, cond := range dep.Status.Conditions {
//         if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
//             return cond.LastTransitionTime.Time
//         }
//     }
    
//     // Fallback to creation time
//     return dep.CreationTimestamp.Time
// }

// func isForDeployment(obj *unstructured.Unstructured, deployName string) bool {
// 	// Check annotations or labels for deployment reference
// 	if ref, found := obj.GetAnnotations()["kubestellar.io/deployment"]; found {
// 		return ref == deployName
// 	}
	
// 	// Check manifest content for ManifestWorks
// 	if manifests, found, _ := unstructured.NestedSlice(obj.Object, "spec", "workload", "manifests"); found {
// 		for _, m := range manifests {
// 			if manifest, ok := m.(map[string]interface{}); ok {
// 				if kind, _, _ := unstructured.NestedString(manifest, "kind"); kind == "Deployment" {
// 					if name, _, _ := unstructured.NestedString(manifest, "metadata", "name"); name == deployName {
// 						return true
// 					}
// 				}
// 			}
// 		}
// 	}
	
// 	// Check applied resources for AppliedManifestWorks
// 	if resources, found, _ := unstructured.NestedSlice(obj.Object, "status", "appliedResources"); found {
// 		for _, r := range resources {
// 			if resource, ok := r.(map[string]interface{}); ok {
// 				if kind, _, _ := unstructured.NestedString(resource, "kind"); kind == "Deployment" {
// 					if name, _, _ := unstructured.NestedString(resource, "name"); name == deployName {
// 						return true
// 					}
// 				}
// 			}
// 		}
// 	}
	
// 	// Check name pattern for WorkStatuses
// 	if strings.Contains(obj.GetName(), deployName) {
// 		return true
// 	}
	
// 	return false
// }

// func containsDeployment(obj *unstructured.Unstructured, deployName, namespace string) bool {
//     // Check direct reference in annotations
//     if ann := obj.GetAnnotations(); ann != nil {
//         if ref, ok := ann["kubestellar.io/deployment"]; ok && ref == deployName {
//             return true
//         }
//     }

//     // Check status.appliedResources
//     resources, found, _ := unstructured.NestedSlice(obj.Object, "status", "appliedResources")
//     if found {
//         for _, r := range resources {
//             res, ok := r.(map[string]interface{})
//             if !ok {
//                 continue
//             }
//             if kind, _, _ := unstructured.NestedString(res, "kind"); kind == "Deployment" {
//                 if name, _, _ := unstructured.NestedString(res, "name"); name == deployName {
//                     if ns, _, _ := unstructured.NestedString(res, "namespace"); ns == namespace {
//                         return true
//                     }
//                 }
//             }
//         }
//     }
    
//     // Check manifest content
//     manifests, found, _ := unstructured.NestedSlice(obj.Object, "spec", "workload", "manifests")
//     if found {
//         for _, m := range manifests {
//             manifest, ok := m.(map[string]interface{})
//             if !ok {
//                 continue
//             }
//             if kind, _, _ := unstructured.NestedString(manifest, "kind"); kind == "Deployment" {
//                 if name, _, _ := unstructured.NestedString(manifest, "metadata", "name"); name == deployName {
//                     return true
//                 }
//             }
//         }
//     }
    
//     return false
// }

// type LatencyData struct {
// 	BindingCreate          time.Time
// 	WDSDeployCreate        time.Time
// 	WDSDeployStatus        time.Time
// 	WECDeployCreate        time.Time
// 	WECDeployStatus        time.Time
// 	ManifestWorkCreate     time.Time
// 	AppliedManifestCreate  time.Time
// 	WorkStatusUpdate       time.Time

// 	BindingTime     time.Duration
// 	PackagingTime   time.Duration
// 	DeliveryTime    time.Duration
// 	ActivationTime  time.Duration
// 	TotalDownsync   time.Duration
// 	ReportTime      time.Duration
// 	Finalization    time.Duration
// 	TotalUpsync     time.Duration
// 	E2ELatency      time.Duration
// }

// // Required for k8s metrics registration
// func (lc *LatencyCollector) Create(version *semver.Version) bool {
//     return true
// }

// func (lc *LatencyCollector) ClearState() {}

// func (lc *LatencyCollector) FQName() string {
//     return "kubestellar_latency_metrics"
// }

// func (lc *LatencyCollector) DeprecatedVersion() *semver.Version {
//     return nil
// }

// func (lc *LatencyCollector) IsDeprecated() bool {
//     return false
// }

// func (lc *LatencyCollector) IsHidden() bool {
//     return false
// }

// func (lc *LatencyCollector) IsCreated() bool {
//     return true
// }

/*
Copyright 2024 The KubeStellar Authors.

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

package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// Updated LatencyCollector with new fields
type LatencyCollector struct {
	wdsClient       kubernetes.Interface
	wecClient       kubernetes.Interface
	wdsDynamic      dynamic.Interface
	itsDynamic      dynamic.Interface
	wecDynamic      dynamic.Interface
	namespace       string
	monitoredDeploy string
	bindingName     string
	itsNamespace    string

	// Downsync metrics
	downsyncBindingTime    *prometheus.Desc
	downsyncPackagingTime  *prometheus.Desc
	downsyncDeliveryTime   *prometheus.Desc
	downsyncActivationTime *prometheus.Desc
	totalDownsyncTime      *prometheus.Desc

	// Upsync metrics
	upsyncReportTime       *prometheus.Desc
	upsyncFinalizationTime *prometheus.Desc
	totalUpsyncTime        *prometheus.Desc

	// E2E latency
	e2eLatency *prometheus.Desc
}

// Updated NewLatencyCollector with new parameters
func NewLatencyCollector(
	wdsClient kubernetes.Interface,
	wecClient kubernetes.Interface,
	wdsDynamic, itsDynamic, wecDynamic dynamic.Interface,
	namespace, monitoredDeploy, bindingName, itsNamespace string,
) *LatencyCollector {
	return &LatencyCollector{
		wdsClient:       wdsClient,
		wecClient:       wecClient,
		wdsDynamic:      wdsDynamic,
		itsDynamic:      itsDynamic,
		wecDynamic:      wecDynamic,
		namespace:       namespace,
		monitoredDeploy: monitoredDeploy,
		bindingName:     bindingName,
		itsNamespace:    itsNamespace,
		
		downsyncBindingTime: prometheus.NewDesc(
			"kubestellar_downsync_binding_time_seconds",
			"Time from binding creation to WDS deployment creation",
			[]string{"namespace", "deployment"}, nil,
		),
		downsyncPackagingTime: prometheus.NewDesc(
			"kubestellar_downsync_packaging_time_seconds",
			"Time from WDS deployment creation to ManifestWork creation",
			[]string{"namespace", "deployment"}, nil,
		),
		downsyncDeliveryTime: prometheus.NewDesc(
			"kubestellar_downsync_delivery_time_seconds",
			"Time from ManifestWork creation to AppliedManifestWork creation",
			[]string{"namespace", "deployment"}, nil,
		),
		downsyncActivationTime: prometheus.NewDesc(
			"kubestellar_downsync_activation_time_seconds",
			"Time from AppliedManifestWork creation to WEC deployment creation",
			[]string{"namespace", "deployment"}, nil,
		),
		totalDownsyncTime: prometheus.NewDesc(
			"kubestellar_total_downsync_time_seconds",
			"Total downsync time from WDS deployment creation to WEC deployment creation",
			[]string{"namespace", "deployment"}, nil,
		),
		upsyncReportTime: prometheus.NewDesc(
			"kubestellar_upsync_report_time_seconds",
			"Time from WEC status update to WorkStatus update",
			[]string{"namespace", "deployment"}, nil,
		),
		upsyncFinalizationTime: prometheus.NewDesc(
			"kubestellar_upsync_finalization_time_seconds",
			"Time from WorkStatus update to WDS status update",
			[]string{"namespace", "deployment"}, nil,
		),
		totalUpsyncTime: prometheus.NewDesc(
			"kubestellar_total_upsync_time_seconds",
			"Total upsync time from WEC status update to WDS status update",
			[]string{"namespace", "deployment"}, nil,
		),
		e2eLatency: prometheus.NewDesc(
			"kubestellar_e2e_latency_seconds",
			"End-to-end latency from WDS creation to status update",
			[]string{"namespace", "deployment"}, nil,
		),
	}
}

// Helper to get status update time from managed fields
func getStatusTime(obj metav1.Object) time.Time {
	for _, mf := range obj.GetManagedFields() {
		if mf.Operation == "Update" && mf.Subresource == "status" && mf.Time != nil {
			return mf.Time.Time
		}
	}
	return time.Time{}
}

// Implement Collect method to gather metrics on each scrape
func (lc *LatencyCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Define GVRs
	bindingPolicyGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "bindingpolicies",
	}
	manifestWorkGVR := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "manifestworks",
	}
	appliedManifestWorkGVR := schema.GroupVersionResource{
		Group:    "work.open-cluster-management.io",
		Version:  "v1",
		Resource: "appliedmanifestworks",
	}
	workStatusGVR := schema.GroupVersionResource{
		Group:    "control.kubestellar.io",
		Version:  "v1alpha1",
		Resource: "workstatuses",
	}

	// Get binding creation time
	binding, err := lc.wdsDynamic.Resource(bindingPolicyGVR).Get(ctx, lc.bindingName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get binding policy %s: %v", lc.bindingName, err)
		return
	}
	bindingCreateTime := binding.GetCreationTimestamp().Time
	klog.Infof("🔗 Binding policy created: %s\n", bindingCreateTime.Format(time.RFC3339Nano))

	// Get WDS deployment
	wdsDeploy, err := lc.wdsClient.AppsV1().Deployments(lc.namespace).Get(ctx, lc.monitoredDeploy, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get WDS deployment %s/%s: %v", lc.namespace, lc.monitoredDeploy, err)
		return
	}
	wdsDeployCreateTime := wdsDeploy.CreationTimestamp.Time
	// wdsDeployStatusTime := getStatusTime(wdsDeploy)
	wdsDeployStatusTime := time.Now() // Placeholder for actual status time logic
	klog.Infof("📦 WDS deployment - Created: %s, Status Updated: %s", wdsDeployCreateTime.Format(time.RFC3339Nano), wdsDeployStatusTime.Format(time.RFC3339Nano))

	// Get ManifestWork
	labelSelector := fmt.Sprintf("transport.kubestellar.io/originOwnerReferenceBindingKey=%s", lc.bindingName)
	klog.Infof("🔍 NameSpace: %s", lc.namespace)
	manifestWorkList, err := lc.itsDynamic.Resource(manifestWorkGVR).Namespace(lc.itsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		klog.Errorf("Failed to list ManifestWorks: %v", err)
		return
	}
	if len(manifestWorkList.Items) == 0 {
		klog.Errorf("No ManifestWorks found for binding '%s' in namespace '%s' with label '%s'", lc.bindingName, lc.itsNamespace, labelSelector)
		return
	}

	manifestWork := manifestWorkList.Items[0]
	manifestWorkCreateTime := manifestWork.GetCreationTimestamp().Time
	klog.Infof("📦 ManifestWork created: %s", manifestWorkCreateTime.Format(time.RFC3339Nano))

	// Get AppliedManifestWork
	appliedManifestWorkList, err := lc.wecDynamic.Resource(appliedManifestWorkGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to get AppliedManifestWork: %v", err)
		return
	}
	// var appliedManifestWork *unstructured.Unstructured
	// for _, item := range appliedManifestWorkList.Items {
	// 	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	// 	if spec["manifestWorkName"] == manifestWork.GetName() && spec["namespace"] == lc.itsNamespace {
	// 		appliedManifestWork = &item
	// 		break
	// 	}
	// }
	// if appliedManifestWork == nil {
	// 	klog.Error("AppliedManifestWork not found")
	// 	return
	// }
	// appliedManifestWorkCreateTime := appliedManifestWork.GetCreationTimestamp().Time
	appliedManifestWorkCreateTime := appliedManifestWorkList.Items[0].GetCreationTimestamp().Time
	klog.Infof("📦 AppliedManifestWork created: %s", appliedManifestWorkCreateTime.Format(time.RFC3339Nano))

	// Get WEC deployment
	wecDeploy, err := lc.wecClient.AppsV1().Deployments(lc.namespace).Get(ctx, lc.monitoredDeploy, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get WEC deployment %s/%s: %v", lc.namespace, lc.monitoredDeploy, err)
		return
	}
	wecDeployCreateTime := wecDeploy.CreationTimestamp.Time
	wecDeployStatusTime := getStatusTime(wecDeploy)
	klog.Infof("📦 WEC deployment - Created: %s, Status Updated: %s", wecDeployCreateTime.Format(time.RFC3339Nano), wecDeployStatusTime.Format(time.RFC3339Nano))

	// Get WorkStatus
	// workStatusName := fmt.Sprintf("v1-deployment-%s-%s", lc.namespace, lc.monitoredDeploy)
	// workStatus, err := lc.itsDynamic.Resource(workStatusGVR).Get(ctx, workStatusName, metav1.GetOptions{})
	// workStatus, err := lc.itsDynamic.Resource(workStatusGVR).Namespace(lc.namespace).List(ctx, metav1.ListOptions{
	// 	LabelSelector: labelSelector,
	// })
	// klog.Infof("🔍 WorkStatus for %s/%s - Found %d items", lc.namespace, lc.monitoredDeploy, len(workStatus.Items))
	// var workStatusUpdateTime time.Time
	// if err == nil && len(workStatus.Items) > 0 {
	// 	workStatusUpdateTime = getStatusTime(&workStatus.Items[0])
	// } else if err != nil {
	// 	klog.Warningf("WorkStatus not available: %v", err)
	// }
	// klog.Infof("🔍 WorkStatus %s - Last Update: %s", workStatusUpdateTime.Format(time.RFC3339Nano))

	suffix := fmt.Sprintf("appsv1-deployment-%s-%s", lc.namespace, lc.monitoredDeploy)

	// List *all* WorkStatus objects (no namespace, no labelSelector)
	wsList, err := lc.itsDynamic.
		Resource(workStatusGVR).
		List(ctx, metav1.ListOptions{})

	if err != nil {
		klog.Errorf("Failed to list WorkStatuses: %v", err)
		return
	}

	// Debug log
	klog.Infof("🔍 WorkStatus (cluster-wide): found %d total items", len(wsList.Items))

	// Look for the one whose name ends with the desired suffix
	var workStatusUpdateTime time.Time
	found := false

	for _, ws := range wsList.Items {
		name := ws.GetName()
		if strings.HasSuffix(name, suffix) {
			// Match found
			workStatusUpdateTime = getStatusTime(&ws)
			klog.Infof("🔍 WorkStatus %q (matched) - Last Update: %s",
				name, workStatusUpdateTime.Format(time.RFC3339Nano))
			found = true
			break
		}
	}

	if !found {
		klog.Warningf(
			"🔍 WorkStatus for %s/%s - Found 0 items (no WorkStatus name ends with %q)",
			lc.namespace, lc.monitoredDeploy, suffix,
		)
		return
	}

	// Calculate and expose metrics
	exposeMetric := func(desc *prometheus.Desc, value float64) {
		ch <- prometheus.MustNewConstMetric(
			desc,
			prometheus.GaugeValue,
			value,
			lc.namespace,
			lc.monitoredDeploy,
		)
	}

	// Downsync metrics
	if bindingTime := wdsDeployCreateTime.Sub(bindingCreateTime).Seconds(); bindingTime > 0 {
		exposeMetric(lc.downsyncBindingTime, bindingTime)
	}
	if packagingTime := manifestWorkCreateTime.Sub(wdsDeployCreateTime).Seconds(); packagingTime > 0 {
		exposeMetric(lc.downsyncPackagingTime, packagingTime)
	}
	if deliveryTime := appliedManifestWorkCreateTime.Sub(manifestWorkCreateTime).Seconds(); deliveryTime > 0 {
		exposeMetric(lc.downsyncDeliveryTime, deliveryTime)
	}
	if activationTime := wecDeployCreateTime.Sub(appliedManifestWorkCreateTime).Seconds(); activationTime > 0 {
		exposeMetric(lc.downsyncActivationTime, activationTime)
	}
	if totalDown := wecDeployCreateTime.Sub(wdsDeployCreateTime).Seconds(); totalDown > 0 {
		exposeMetric(lc.totalDownsyncTime, totalDown)
	}

	// Upsync metrics
	if !workStatusUpdateTime.IsZero() {
		if reportTime := workStatusUpdateTime.Sub(wecDeployStatusTime).Seconds(); reportTime > 0 {
			exposeMetric(lc.upsyncReportTime, reportTime)
		}
		if finalizationTime := wdsDeployStatusTime.Sub(workStatusUpdateTime).Seconds(); finalizationTime > 0 {
			exposeMetric(lc.upsyncFinalizationTime, finalizationTime)
		}
	}
	if totalUp := wdsDeployStatusTime.Sub(wecDeployStatusTime).Seconds(); totalUp > 0 {
		exposeMetric(lc.totalUpsyncTime, totalUp)
	}

	// E2E latency
	if e2e := wdsDeployStatusTime.Sub(wdsDeployCreateTime).Seconds(); e2e > 0 {
		exposeMetric(lc.e2eLatency, e2e)
	}
}

// Describe method remains the same
func (lc *LatencyCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lc.downsyncBindingTime
	ch <- lc.downsyncPackagingTime
	ch <- lc.downsyncDeliveryTime
	ch <- lc.downsyncActivationTime
	ch <- lc.totalDownsyncTime
	ch <- lc.upsyncReportTime
	ch <- lc.upsyncFinalizationTime
	ch <- lc.totalUpsyncTime
	ch <- lc.e2eLatency
}

// Required for k8s metrics registration
func (lc *LatencyCollector) Create(version *semver.Version) bool {
    return true
}

func (lc *LatencyCollector) ClearState() {}

func (lc *LatencyCollector) FQName() string {
    return "kubestellar_latency_metrics"
}

func (lc *LatencyCollector) DeprecatedVersion() *semver.Version {
    return nil
}

func (lc *LatencyCollector) IsDeprecated() bool {
    return false
}

func (lc *LatencyCollector) IsHidden() bool {
    return false
}

func (lc *LatencyCollector) IsCreated() bool {
    return true
}
