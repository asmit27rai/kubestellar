## Steps:

```bash
make build
```

```bash
./bin/controller-manager   --kubeconfig ~/.kube/config   --wds-name wds1(your wds name)   --its-name its1(your its name)   --monitored-deployment=nginx-deployment(your deployment name)   --monitored-namespace=nginx(your namespace) --binding-name=nginx-bpolicy(binding policy name) --wec-context=cluster1  -v=5
```

```bash
curl http://localhost:2222/metrics | grep kubestellar
```