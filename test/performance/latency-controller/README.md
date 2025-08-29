# Framework to Benchmark KubeStellar Data-Plane Latencies

<p align="center">
  <img src="images/Diagram.png" width="700" height="300" title="Benchmarking of KubeStellar  Data-Plane ">
</p>

## Overview

The KubeStellar Latency Controller is a comprehensive monitoring solution designed to measure and track end-to-end latencies across multi-cluster workload deployments in KubeStellar environments. It provides detailed metrics collection and observability for the complete workload deployment pipeline from Workload Definition Space (WDS) to Workload Execution Clusters (WECs).

## Architecture

The following steps describe the end-to-end workflow of object creation, delivery, and status propagation:

1. **WDS Object Created**
   - Initial object is created in the WDS cluster.

2. **📦 Packaging → ManifestWork Created (ITS)**
   - The workload is packaged into a `ManifestWork` object in the ITS cluster.

3. **📬 Delivery → AppliedManifestWork Created (WEC)**
   - The packaged work is delivered, resulting in an `AppliedManifestWork` in the WEC cluster.

4. **🏭 Activation → Workload Object Created (WEC)**
   - The workload object (e.g., Deployment, Service) is created in the WEC cluster.

5. **📝 Status Collection → WorkStatus Created (ITS)**
   - The WEC cluster reports status back to the ITS cluster through a `WorkStatus` object.

6. **✅ Status Propagation → WDS Object Status Updated**
   - The collected status is propagated back and reflected in the original WDS object.

## Features

### Comprehensive Latency Metrics

The controller tracks multiple stages of the deployment pipeline:

- **📦 Packaging Duration**: Time from WDS object creation to ManifestWork creation  
- **📬 Delivery Duration**: Time from ManifestWork to AppliedManifestWork creation  
- **🏭 Activation Duration**: Time from AppliedManifestWork to WEC object creation  
- **⚡ Total Downsync Duration**: End-to-end time from WDS to WEC object creation  
- **📝 Status Propagation**: Time for status to flow back from WEC to WDS  
- **🔄 End-to-End Latency**: Complete cycle from workload creation to status update  

### Advanced Monitoring Capabilities

- **Multi-Cluster Support**: Monitors workloads across multiple WEC clusters simultaneously  
- **Dynamic Resource Discovery**: Automatically discovers and monitors all configured Kubernetes resource types  
- **Real-time Metrics**: Provides live latency measurements via Prometheus metrics  
- **Workload Count Tracking**: Monitors the number of deployed workload objects per cluster  
- **Status Field Detection**: Intelligently handles resources with and without status fields  

## Prerequisites

Before deploying the Latency Controller, ensure you have:

1. **KubeStellar Environment**: You must have an environment with KubeStellar installed; see [KubeStellar getting started](https://docs.kubestellar.io/release-0.23.1/direct/get-started/). Alternatively, you can also use KubeStellar e2e script [run-test.sh](https://github.com/kubestellar/kubestellar/blob/main/test/e2e/run-test.sh) to setup an environment.

2. **KubeStellar Monitoring Setup**: The base monitoring infrastructure must be installed first:
   - Setup the KS monitoring tool [Here](https://github.com/kubestellar/kubestellar/tree/main/monitoring#readme)
   - This installs Prometheus and Grafana components

## Installation

### Step 1: Build and Push Controller Image

Build and push the controller image to your container registry:
```bash
Build the controller image
make docker-copy
make docker-build IMAGE=<your-registry>/latency-controller:tag

Push to registry
make docker-push IMAGE=<your-registry>/latency-controller:tag
```

### Step 2: Configure Latency Controller
```bash
# 1. Create latency-collector service
sed s/%WDS%/wds1/g configuration/latency-collector-svc.yaml | kubectl -n wds1-system apply -f -

# 2. Create latency-collector service monitor
sed s/%WDS%/wds1/g configuration/latency-collector-sm.yaml | kubectl -n ks-monitoring apply -f -
```

### Step 3: Deploy the Latency Controller

Use the deployment script to install the controller:
```bash
./deploy-latency-controller.sh
--latency_controller_image "<CONTROLLER_IMAGE>"
--binding-policy-name "<BINDING_Policy_NAME>"
--monitored-namespace "<NAMESPACE_TO_MONITOR>"
--host-context "<HOST_KUBECONFIG_CONTEXT>"
--wds-context "<WDS_CONTEXT>"
--its-context "<ITS_CONTEXT>"
--wec-files "<WEC_NAME1>:<PATH_TO_KUBECONFIG1>,<WEC_NAME2>:<PATH_TO_KUBECONFIG2>"
[--image-pull-policy "<PULL_POLICY>"]
[--controller-verbosity "<VERBOSITY_LEVEL>"]
[--wds-incluster-file "<PATH_TO_WDS_INCLUSTER_CONFIG>"]
[--its-incluster-file "<PATH_TO_ITS_INCLUSTER_CONFIG>"]
```

### Configuration Parameters

| Parameter                   | Description                                                | Required | Default  |
|-----------------------------|------------------------------------------------------------|----------|----------|
| `--latency_controller_image`| Container image for the latency controller                 | ✅        | -        |
| `--binding-name`          | Name of the BindingPolicy to monitor                       | ✅        | -        |
| `--monitored-namespace`     | Namespace to monitor for workloads                         | ✅        | -        |
| `--host-context`            | Kubeconfig context for KubeFlex hosting cluster            | ✅        | -        |
| `--wds-context`             | Kubeconfig context for WDS                                 | ✅        | -        |
| `--its-context`             | Kubeconfig context for ITS                                 | ✅        | -        |
| `--wec-files`               | Comma-separated list of WEC configs (name:path)            | ✅        | -        |
| `--image-pull-policy`       | Image pull policy for the controller                       | ❌        | `Always` |
| `--controller-verbosity`    | Log verbosity level (0-10)                                 | ❌        | `2`      |
| `--wds-incluster-file`      | Path to WDS in-cluster config                              | ❌        | -        |
| `--its-incluster-file`      | Path to ITS in-cluster config                              | ❌        | -        |

Check The latency controller service:
```
kubectl get pods -n wds1-system | grep "latency-controller"
```

Output:
```
NAME                                            READY   STATUS    RESTARTS   AGE
latency-controller-867f84f4cf-tdl8d             1/1     Running   0          62s
```

### Step 3: Import KubeStellar Grafana dashboards into the Grafana UI as in Monitoring Tool:

Grafana Dashboard

After deploying the Latency Controller, import the provided Grafana dashboard to visualize the metrics:

1. **Access Grafana**: Connect to your Grafana instance (deployed via KS monitoring setup)  
2. **Import Dashboard**: Use the provided dashboard JSON configuration  
3. **Configure Data Source**: Ensure Prometheus is configured as a data source  
4. **View Metrics**: Monitor latency patterns, identify bottlenecks, and track performance trends 

Import `kubestellar-dashboard.json`

You Can See:
<p align="center">
  <img src="images/Grafana.png" width="800" height="400" title="KS-Latency-Controller">
</p>

## Metrics

The Latency Controller exposes the following Prometheus metrics:

- **📦 Packaging Duration**: Time from WDS object creation to ManifestWork creation  
- **📬 Delivery Duration**: Time from ManifestWork to AppliedManifestWork creation  
- **🏭 Activation Duration**: Time from AppliedManifestWork to WEC object creation  
- **⚡ Total Downsync Duration**: End-to-end time from WDS to WEC object creation  
- **📝 Status Propagation**: Time for status to flow back from WEC to WDS  
- **🔄 End-to-End Latency**: Complete cycle from workload creation to status update  

### Histogram Metrics

All histogram metrics include labels: `workload`, `cluster`, `kind`, `apiVersion`, `namespace`, `bindingpolicy`

- `kubestellar_downsync_packaging_duration_seconds`  
- `kubestellar_downsync_delivery_duration_seconds`  
- `kubestellar_downsync_activation_duration_seconds`  
- `kubestellar_downsync_duration_seconds`  
- `kubestellar_statusPropagation_report_duration_seconds`  
- `kubestellar_statusPropagation_finalization_duration_seconds`  
- `kubestellar_statusPropagation_duration_seconds`  
- `kubestellar_e2e_latency_duration_seconds`  

### Gauge Metrics

- `kubestellar_workload_count`  
  - Labels: `cluster`, `kind`, `apiVersion`, `namespace`, `bindingName` 
