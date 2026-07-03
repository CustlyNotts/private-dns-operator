#!/usr/bin/env bash
set -euo pipefail

ZONE_NAME="${ZONE_NAME:-rancher}"
ZONE_DOMAIN="${ZONE_DOMAIN:-rancher.io}"
RECORD_NAME="${RECORD_NAME:-git}"
PRIVATE_IP_ONE="${PRIVATE_IP_ONE:-10.250.200.10}"
PRIVATE_IP_TWO="${PRIVATE_IP_TWO:-10.250.200.11}"
FALLTHROUGH_NAME="${FALLTHROUGH_NAME:-www.rancher.io}"
TEST_NAMESPACE="${TEST_NAMESPACE:-default}"
TIMEOUT="${TIMEOUT:-120s}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

apply_manifest() {
  kubectl apply -f - >/dev/null
}

lookup_from_cluster() {
  local name="$1"
  kubectl -n "$TEST_NAMESPACE" run private-dns-lookup \
    --image=busybox:1.36 \
    --restart=Never \
    --rm -i \
    --command -- nslookup "$name" 2>&1
}

wait_for_zone_ready() {
  kubectl wait --for=condition=Ready "privatednszone/${ZONE_NAME}" --timeout="$TIMEOUT"
}

cleanup() {
  kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-one" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-two" --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete privatednszone "$ZONE_NAME" --ignore-not-found >/dev/null 2>&1 || true
}

need kubectl
trap cleanup EXIT

cat <<YAML | apply_manifest
apiVersion: dns.custlynotts.io/v1alpha1
kind: PrivateDNSZone
metadata:
  name: ${ZONE_NAME}
spec:
  zone: ${ZONE_DOMAIN}
  ttl: 60
  unresolvedRecordPolicy: Forward
  allowedNamespaces: {}
YAML

cat <<YAML | apply_manifest
apiVersion: dns.custlynotts.io/v1alpha1
kind: PrivateDNSRecord
metadata:
  name: ${ZONE_NAME}-${RECORD_NAME}-one
  namespace: ${TEST_NAMESPACE}
spec:
  zoneRef:
    name: ${ZONE_NAME}
  name: ${RECORD_NAME}
  type: A
  ttl: 60
  values:
    - ${PRIVATE_IP_ONE}
YAML

cat <<YAML | apply_manifest
apiVersion: dns.custlynotts.io/v1alpha1
kind: PrivateDNSRecord
metadata:
  name: ${ZONE_NAME}-${RECORD_NAME}-two
  namespace: ${TEST_NAMESPACE}
spec:
  zoneRef:
    name: ${ZONE_NAME}
  name: ${RECORD_NAME}
  type: A
  ttl: 60
  values:
    - ${PRIVATE_IP_TWO}
YAML

wait_for_zone_ready

private_name="${RECORD_NAME}.${ZONE_DOMAIN}"
private_output="$(lookup_from_cluster "$private_name")"
echo "$private_output"
if [[ "$private_output" != *"${PRIVATE_IP_ONE}"* || "$private_output" != *"${PRIVATE_IP_TWO}"* ]]; then
  echo "expected ${private_name} to resolve to ${PRIVATE_IP_ONE} and ${PRIVATE_IP_TWO}" >&2
  exit 1
fi

fallthrough_output="$(lookup_from_cluster "$FALLTHROUGH_NAME")"
echo "$fallthrough_output"
if [[ "$fallthrough_output" == *"NXDOMAIN"* || "$fallthrough_output" == *"can't resolve"* ]]; then
  echo "expected ${FALLTHROUGH_NAME} to fall through to upstream DNS" >&2
  exit 1
fi

kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-one" >/dev/null
wait_for_zone_ready
stale_output="$(lookup_from_cluster "$private_name")"
echo "$stale_output"
if [[ "$stale_output" == *"${PRIVATE_IP_ONE}"* ]]; then
  echo "stale record ${PRIVATE_IP_ONE} was still returned after deleting its PrivateDNSRecord" >&2
  exit 1
fi
if [[ "$stale_output" != *"${PRIVATE_IP_TWO}"* ]]; then
  echo "remaining record ${PRIVATE_IP_TWO} was not returned after stale cleanup" >&2
  exit 1
fi

echo "private DNS smoke test passed"
