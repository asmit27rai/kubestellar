## Steps:

```bash
make build
```

```bash
./bin/controller-manager   --kubeconfig ~/.kube/config   --wds-name wds1   --its-name its1   --monitored-deployment=nginx-deployment   --monitored-namespace=nginx --binding-name=nginx-bpolicy --wec-context=cluster1  -v=5
```

```bash
curl http://localhost:2222/metrics | grep kubestellar
```