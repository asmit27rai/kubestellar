# KubeStellar Latency Controller

<p align="center">
  <img src="images/Architecture.png" width="700" height="300" title="Latency-Controller-Architecture">
</p>

## Overview

The KubeStellar Latency Controller is a comprehensive monitoring solution designed to measure and track end-to-end latencies across multi-cluster workload deployments in KubeStellar environments. It provides detailed metrics collection and observability for the complete workload deployment pipeline from Workload Definition Space (WDS) to Workload Execution Clusters (WECs).

## Architecture

![KubeStellar Latency Controller monitoring workflow and architecture overview][22]

The Latency Controller monitors the complete workload deployment and status propagation lifecycle:

1. **WDS Object Creation** → workload object created in Workload Definition Space  
2. **Packaging** → ManifestWork creation from WDS objects  
3. **Delivery** → ManifestWork transported to cluster namespaces  
4. **Activation** → AppliedManifestWork created on WEC clusters  
5. **WEC Object Creation** → actual workload objects deployed on target clusters  
6. **Status Propagation** → status flows back from WEC to WDS via WorkStatus objects  

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

1. **KubeStellar Environment**: A working KubeStellar setup with:
   - KubeFlex hosting cluster  
   - Workload Definition Space (WDS)  
   - Inventory and Transport Space (ITS)  
   - One or more registered Workload Execution Clusters (WECs)  

2. **KubeStellar Monitoring Setup**: The base monitoring infrastructure must be installed first:
   - Setup the KS monitoring tool [Here](https://github.com/kubestellar/kubestellar/tree/main/monitoring#readme)
   - This installs Prometheus and Grafana components

## Installation

### Step 1: Build and Push Controller Image

Build and push the controller image to your container registry:
```bash
Build the controller image
make docker-build IMAGE=<your-registry>/latency-controller:tag

Push to registry
make docker-push IMAGE=<your-registry>/latency-controller:tag
```

### Step 2: Deploy the Latency Controller

Use the deployment script to install the controller:
```bash
./deploy-latency-controller.sh
--latency_controller_image "<CONTROLLER_IMAGE>"
--binding-policy "<BINDING_POLICY>"
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

Import `kubestellar-dashboard.json`

You Can See:
<p align="center">
  <img src="images/Grafana.png" width="500" height="250" title="KS-Latency-Controller">
</p>


## Configuration Parameters

| Parameter                   | Description                                                | Required | Default  |
|-----------------------------|------------------------------------------------------------|----------|----------|
| `--latency_controller_image`| Container image for the latency controller                 | ✅        | -        |
| `--binding-policy`          | Name of the BindingPolicy to monitor                       | ✅        | -        |
| `--monitored-namespace`     | Namespace to monitor for workloads                         | ✅        | -        |
| `--host-context`            | Kubeconfig context for KubeFlex hosting cluster            | ✅        | -        |
| `--wds-context`             | Kubeconfig context for WDS                                 | ✅        | -        |
| `--its-context`             | Kubeconfig context for ITS                                 | ✅        | -        |
| `--wec-files`               | Comma-separated list of WEC configs (name:path)            | ✅        | -        |
| `--image-pull-policy`       | Image pull policy for the controller                       | ❌        | `Always` |
| `--controller-verbosity`    | Log verbosity level (0-10)                                 | ❌        | `2`      |
| `--wds-incluster-file`      | Path to WDS in-cluster config                              | ❌        | -        |
| `--its-incluster-file`      | Path to ITS in-cluster config                              | ❌        | -        |

## Metrics

The Latency Controller exposes the following Prometheus metrics:

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
  - Labels: `cluster`, `kind`, `apiVersion`, `namespace`, `bindingpolicy`

## Grafana Dashboard

After deploying the Latency Controller, import the provided Grafana dashboard to visualize the metrics:

1. **Access Grafana**: Connect to your Grafana instance (deployed via KS monitoring setup)  
2. **Import Dashboard**: Use the provided dashboard JSON configuration  
3. **Configure Data Source**: Ensure Prometheus is configured as a data source  
4. **View Metrics**: Monitor latency patterns, identify bottlenecks, and track performance trends  

## How It Works

### Controller Architecture

The Latency Controller implements a sophisticated monitoring system:

1. **Dynamic Resource Discovery**  
2. **Event-Driven Processing**  
3. **Multi-Cluster Coordination**  
4. **Intelligent Caching**  
5. **Prometheus Integration**  

## 📊 Monitoring Workflow

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

### Resource Type Support

The controller automatically handles different Kubernetes resource types:

- **Stateful Resources**: Deployments, StatefulSets, DaemonSets, Pods, Services, etc.  
- **Stateless Resources**: ConfigMaps, Secrets, ServiceAccounts, RBAC objects, etc.  
- **Custom Resources**: Any CRDs deployed in your clusters  