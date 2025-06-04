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
