# kubestellar-latency-collector

A Kubernetes controller that measures and exposes latency metrics for KubeStellar's downsync and upsync operations.

## Here are deployments:
```bash
bash <(curl -s https://raw.githubusercontent.com/kubestellar/kubestellar/refs/tags/v0.27.2/scripts/create-kubestellar-demo-env.sh) --platform kind

export host_context=kind-kubeflex
export its_cp=its1
export its_context=its1
export wds_cp=wds1
export wds_context=wds1
export wec1_name=cluster1
export wec2_name=cluster2
export wec1_context=cluster1
export wec2_context=cluster2
export label_query_both="location-group=edge"
export label_query_one="name=cluster1"

kubectl --context "$wds_context" apply -f - <<EOF
apiVersion: control.kubestellar.io/v1alpha1
kind: BindingPolicy
metadata:
  name: nginx-singleton-bpolicy
spec:
  clusterSelectors:
  - matchLabels: {"name":"cluster1"}
  downsync:
  - objectSelectors:
    - matchLabels: {"app.kubernetes.io/name":"nginx-singleton"}
    wantSingletonReportedState: true
EOF

kubectl --context "$wds_context" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-singleton-deployment
  namespace: default  # Explicit namespace
  labels:
    app.kubernetes.io/name: nginx-singleton
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:alpine  # Using standard Docker Hub image
        ports:
        - containerPort: 80
        readinessProbe:       # Added readiness probe
          httpGet:
            path: /
            port: 80
          initialDelaySeconds: 5
          periodSeconds: 5
EOF

kubectl --context "$wds_context" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-test-deployment
  namespace: default  # Explicit namespace
  labels:
    app.kubernetes.io/name: nginx-singleton
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:alpine  # Using standard Docker Hub image
        ports:
        - containerPort: 80
        readinessProbe:       # Added readiness probe
          httpGet:
            path: /
            port: 80
          initialDelaySeconds: 5
          periodSeconds: 5
EOF
```

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
  --wds-context=wds1 \
  --wec-contexts=cluster1 \
  --its-context=its1 \
  --monitored-namespace=default \
  --binding-name=nginx-singleton-bpolicy \
  --leader-elect=false \
  --metrics-bind-address=:2222
```
### In different terminal 

```bash
# Download Prometheus
wget https://github.com/prometheus/prometheus/releases/download/v2.51.0/prometheus-2.51.0.linux-amd64.tar.gz
tar -xzf prometheus-2.51.0.linux-amd64.tar.gz
cd prometheus-2.51.0.linux-amd64
```

```bash
# Add Configuration file
# Create a `prometheus.yml` file in the Prometheus directory with the following content:
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'kubestellar-latency'
    static_configs:
      - targets: ['localhost:2222']
    metrics_path: '/metrics'
```

```bash
./prometheus \
  --config.file=prometheus.yml \
  --storage.tsdb.path=./data \
  --web.listen-address=0.0.0.0:9090
```

### In another terminal

```bash
# Download Grafana
wget https://dl.grafana.com/oss/release/grafana-10.4.1.linux-amd64.tar.gz
tar -xzf grafana-10.4.1.linux-amd64.tar.gz
cd grafana-10.4.1
```

```bash
# Run this script
./bin/grafana-server web
```

- Go to localhost:3000
- Create dashboard with json `./kubestellar-dashboard.json`.