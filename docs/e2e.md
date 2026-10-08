# E2E Smoke Test

This guide verifies the operator against a real Kubernetes cluster with CoreDNS.

## What It Proves

The smoke test checks that:

- a `PrivateDNSZone` becomes `Ready`
- compatible duplicate `A` records are returned for one FQDN
- `unresolvedRecordPolicy: Forward` falls through to upstream DNS for undeclared names
- deleting one `PrivateDNSRecord` removes only that record from DNS output
- zone cleanup runs when the test exits

## Prerequisites

- `kubectl` points at a cluster running CoreDNS
- the CRDs and operator are installed
- CoreDNS pods are healthy
- the cluster can resolve the public fallback name, by default `www.rancher.io`

## Run

```bash
./e2e/smoke-test.sh
```

Each DNS assertion polls rather than sampling once. A zone reporting `Ready` only
means the operator has written the CoreDNS ConfigMap; the kubelet then has to
re-project the volume and CoreDNS has to reload, which is routinely 45 to 90
seconds. Tune the patience with `DNS_TIMEOUT` (default 180 seconds) and
`DNS_POLL_INTERVAL` (default 10).

Override defaults when needed:

```bash
ZONE_NAME=corp \
ZONE_DOMAIN=example.com \
RECORD_NAME=api \
PRIVATE_IP_ONE=10.0.0.10 \
PRIVATE_IP_TWO=10.0.0.11 \
FALLTHROUGH_NAME=www.example.com \
./e2e/smoke-test.sh
```

## Manual Checks

Inspect the generated CoreDNS block:

```bash
kubectl -n kube-system get configmap coredns -o yaml
```

You should see a managed block like:

```text
# BEGIN private-dns-zone-operator

example.com:53 {
    errors
    template IN A example.com {
        match ^api\.example\.com\.$
        answer "{{ .Name }} 60 IN A 10.0.0.10"
        fallthrough
    }
    forward . /etc/resolv.conf
    cache 30
}
# END private-dns-zone-operator
```
