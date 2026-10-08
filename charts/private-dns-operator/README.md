# private-dns-operator

Kubernetes-native private DNS zone management for CoreDNS.

The operator turns `PrivateDNSZone` and `PrivateDNSRecord` resources into CoreDNS
configuration, writing only between its own markers in the Corefile. See the
[project README](https://github.com/CustlyNotts/private-dns-operator) for the API
reference and DNS semantics.

## Install

```bash
helm upgrade --install private-dns-operator \
  oci://ghcr.io/custlynotts/charts/private-dns-operator \
  --version 1.0.1 \
  --namespace private-dns-operator-system \
  --create-namespace
```

The chart installs its CRDs from `crds/`. Helm does not upgrade or delete CRDs
on `helm upgrade` or `helm uninstall`, so apply CRD changes yourself when a
release notes them:

```bash
kubectl apply -f https://raw.githubusercontent.com/CustlyNotts/private-dns-operator/v1.0.1/config/crd/bases/dns.custlynotts.io_privatednszones.yaml
kubectl apply -f https://raw.githubusercontent.com/CustlyNotts/private-dns-operator/v1.0.1/config/crd/bases/dns.custlynotts.io_privatednsrecords.yaml
```

## Requirements

- Kubernetes 1.27 or later
- CoreDNS running in the cluster, with a ConfigMap the operator may patch
- Helm 3.8 or later for OCI chart support

## RBAC

With `rbac.create` enabled the chart creates:

- a `ClusterRole` for the CRDs, their status and finalizers, events, and leases
- a namespaced `Role` in `coredns.namespace`, restricted with `resourceNames` to
  exactly `coredns.configMap` and `coredns.deployment`

Changing `coredns.configMap` or `coredns.deployment` moves that `resourceNames`
scope with it, so no manual RBAC edit is needed.

Note that anything able to create a `PrivateDNSZone` can change cluster-wide DNS
resolution. Treat the CRD as a cluster-admin resource and restrict it with your
own RBAC. See [SECURITY.md](https://github.com/CustlyNotts/private-dns-operator/blob/main/SECURITY.md).

## Values

| Key | Type | Default | Description |
| --- | ---- | ------- | ----------- |
| `replicaCount` | int | `1` | Operator replicas. Leader election keeps one active, so extra replicas buy failover, not throughput. |
| `image.repository` | string | `ghcr.io/custlynotts/private-dns-operator` | Operator image repository. |
| `image.tag` | string | `""` | Image tag. Empty tracks the chart's `appVersion`; pinning it here overrides that and can hold an old image across upgrades. |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | list | `[]` | Pull secrets for a private registry. |
| `nameOverride` | string | `""` | Overrides the chart name used in resource names. |
| `fullnameOverride` | string | `""` | Overrides the full resource name. |
| `serviceAccount.create` | bool | `true` | Create a ServiceAccount for the operator. |
| `serviceAccount.annotations` | object | `{}` | Annotations for the ServiceAccount. |
| `serviceAccount.name` | string | `""` | Name of an existing ServiceAccount to use. |
| `podAnnotations` | object | `{}` | Annotations on the operator pod. |
| `podLabels` | object | `{}` | Extra labels on the operator pod. |
| `podSecurityContext` | object | non-root, `RuntimeDefault` seccomp | Pod-level security context. |
| `securityContext` | object | no privilege escalation, all capabilities dropped, read-only root | Container-level security context. |
| `resources` | object | requests `10m`/`64Mi`, limit `256Mi` memory | Container resources. Raise the memory limit for a large number of zones. |
| `nodeSelector` | object | `{}` | Node selector for the operator pod. |
| `tolerations` | list | `[]` | Tolerations for the operator pod. |
| `affinity` | object | `{}` | Affinity rules for the operator pod. |
| `leaderElection.enabled` | bool | `true` | Enable leader election. Required if `replicaCount` is above 1, or several managers will write the CoreDNS ConfigMap at once. |
| `metrics.bindAddress` | string | `":8080"` | Address the metrics endpoint binds to. |
| `metrics.service.enabled` | bool | `false` | Create a Service for metrics. Off by default because the endpoint is unauthenticated. |
| `metrics.service.type` | string | `ClusterIP` | Metrics Service type. |
| `metrics.service.port` | int | `8080` | Metrics Service port. |
| `metrics.service.annotations` | object | `{}` | Annotations on the metrics Service. |
| `metrics.serviceMonitor.enabled` | bool | `false` | Create a Prometheus Operator ServiceMonitor. Requires `metrics.service.enabled`. |
| `metrics.serviceMonitor.interval` | string | `30s` | Scrape interval. |
| `metrics.serviceMonitor.scrapeTimeout` | string | `10s` | Scrape timeout. |
| `metrics.serviceMonitor.labels` | object | `{}` | Extra labels, for matching your Prometheus `serviceMonitorSelector`. |
| `healthProbe.bindAddress` | string | `":8081"` | Address the health and readiness probes bind to. |
| `coredns.namespace` | string | `kube-system` | Namespace holding the CoreDNS resources. |
| `coredns.configMap` | string | `coredns` | CoreDNS ConfigMap the operator patches. |
| `coredns.deployment` | string | `coredns` | CoreDNS Deployment the operator patches and restarts. |
| `rbac.create` | bool | `true` | Create the ClusterRole, Role, and bindings. |
| `extraArgs` | list | `[]` | Extra manager flags, for example `["--zap-log-level=debug"]`. |
| `extraEnv` | list | `[]` | Extra container environment variables, in standard Kubernetes `env` form. |

## Targeting a non-default CoreDNS

```bash
helm upgrade --install private-dns-operator \
  oci://ghcr.io/custlynotts/charts/private-dns-operator \
  --namespace private-dns-operator-system --create-namespace \
  --set coredns.namespace=dns-system \
  --set coredns.configMap=coredns-custom \
  --set coredns.deployment=coredns-custom
```

## Uninstall

```bash
helm uninstall private-dns-operator --namespace private-dns-operator-system
```

Delete the `PrivateDNSZone` objects **before** uninstalling. Each zone carries a
finalizer that the operator releases as it cleans the zone out of CoreDNS; with
the operator already gone, those objects will not delete, the managed Corefile
block is left behind, and the CRD cannot be removed either. The
[project README](https://github.com/CustlyNotts/private-dns-operator#uninstall)
has the manual recovery steps if you hit it.

Helm leaves the CRDs in place. Remove them once no zones remain:

```bash
kubectl delete crd privatednszones.dns.custlynotts.io privatednsrecords.dns.custlynotts.io
```
