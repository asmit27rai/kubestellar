# kubestellar-latency-collector

A Kubernetes controller that measures and exposes latency metrics for KubeStellar's downsync and upsync operations.

## Features
- Measures 9 key latency metrics for KubeStellar operations
-Exposes Prometheus metrics endpoint
-Supports multi-cluster environments
-Easy deployment with Kustomize
-Configurable through command-line flags

## Install Dependencies
```bash
go mod tidy
```

## Build the Binary
```bash
go build -o bin/latency-collector cmd/main.go
```

## Run Locally
```bash
./bin/latency-collector \
  --kubeconfig=~/.kube/config \
  --wds-context=your-wds-context \
  --wec-context=your-wec-context \
  --its-context=your-its-context \
  --monitored-namespace=namespace \
  --monitored-deployment=your-deployment \
  --binding-name=your-binding \
  --leader-elect=false \
  --metrics-bind-address=:2222
```

## Access Metrics
```bash
curl localhost:2222/metrics | grep kubestellar
```