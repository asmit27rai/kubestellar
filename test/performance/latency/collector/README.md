# Latency Collector: Quick Setup & Run

A minimal guide to build and run the Kubestellar latency collector, which exposes Prometheus metrics on port 2222.

---

## 1. Prerequisites

- Go 1.18+ installed  
- A valid kubeconfig (e.g. `~/.kube/config`) with access to:
  - `bindingpolicies` in `control.kubestellar.io/v1alpha1`
  - `manifestworks` / `appliedmanifestworks` in `work.open-cluster-management.io/v1`
  - Deployments in `apps/v1`
- `kubectl` configured and able to `kubectl get deployment` in your clusters
- Port 2222 free on localhost

---

## Testing

- Run the `collector.go` file:
```bash
./collector \
  --kubeconfig ~/.kube/config \
  --monitored-deployment=<your-deployment> \
  --monitored-namespace=<namespace> \
  --binding-name=<binding-policy-name> \
  --wec-context=<wec-cluster-context> \
  --wds-context=<wds-cluster-context> \ --its-context=<its-cluster-context>
``