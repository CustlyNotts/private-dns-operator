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

# A zone reporting Ready only means the operator has finished writing the CoreDNS
# ConfigMap. Two further delays stand between that and DNS actually changing: the
# kubelet re-projects the ConfigMap volume into the CoreDNS pods, and CoreDNS's
# reload plugin then has to notice the new Corefile. On a stock cluster that is
# routinely 45 to 90 seconds in total.
#
# Every DNS assertion below therefore polls to a deadline rather than sampling
# once. Sampling once makes this script fail against a perfectly healthy
# operator, because the old answer is still cached and still being served.
DNS_TIMEOUT="${DNS_TIMEOUT:-180}"
DNS_POLL_INTERVAL="${DNS_POLL_INTERVAL:-10}"
LOOKUP_POD="${LOOKUP_POD:-private-dns-lookup}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

apply_manifest() {
  kubectl apply -f - >/dev/null
}

delete_lookup_pod() {
  kubectl -n "$TEST_NAMESPACE" delete pod "$LOOKUP_POD" --ignore-not-found --now >/dev/null 2>&1 || true
}

lookup_from_cluster() {
  local name="$1"
  # Clear any pod left behind by an interrupted run, otherwise the next run
  # fails with AlreadyExists rather than reporting a DNS result.
  delete_lookup_pod
  kubectl -n "$TEST_NAMESPACE" run "$LOOKUP_POD" \
    --image=busybox:1.36 \
    --restart=Never \
    --rm -i \
    --command -- nslookup "$name" 2>&1
}

# await_lookup NAME DESCRIPTION PREDICATE
#
# Repeatedly resolves NAME from inside the cluster until PREDICATE accepts the
# output, printing the accepted output. Fails once DNS_TIMEOUT elapses, printing
# the last output seen so the failure is diagnosable.
await_lookup() {
  local name="$1" description="$2" predicate="$3"
  local deadline=$(( $(date +%s) + DNS_TIMEOUT ))
  local output

  while :; do
    output="$(lookup_from_cluster "$name")"
    if "$predicate" "$output"; then
      printf '%s\n' "$output"
      return 0
    fi
    if (( $(date +%s) >= deadline )); then
      echo "--- last lookup output for ${name} ---" >&2
      printf '%s\n' "$output" >&2
      echo "timed out after ${DNS_TIMEOUT}s waiting for ${description}" >&2
      return 1
    fi
    sleep "$DNS_POLL_INTERVAL"
  done
}

serves_both_private_ips() {
  [[ "$1" == *"${PRIVATE_IP_ONE}"* && "$1" == *"${PRIVATE_IP_TWO}"* ]]
}

resolved_upstream() {
  [[ "$1" != *NXDOMAIN* && "$1" != *"can't resolve"* ]]
}

stale_record_cleared() {
  [[ "$1" != *"${PRIVATE_IP_ONE}"* && "$1" == *"${PRIVATE_IP_TWO}"* ]]
}

wait_for_zone_ready() {
  kubectl wait --for=condition=Ready "privatednszone/${ZONE_NAME}" --timeout="$TIMEOUT"
}

cleanup() {
  delete_lookup_pod
  kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-one" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-two" --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete privatednszone "$ZONE_NAME" --ignore-not-found >/dev/null 2>&1 || true
}

need kubectl
trap cleanup EXIT

echo "==> applying zone ${ZONE_NAME} (${ZONE_DOMAIN}) and two ${RECORD_NAME} records"

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

echo "==> waiting for ${private_name} to return both private addresses"
# This also proves the records are merged into one RRset. Rendering them as
# separate CoreDNS template stanzas would serve only the first address, because
# CoreDNS answers from the first stanza whose match expression hits.
await_lookup "$private_name" \
  "${private_name} to resolve to ${PRIVATE_IP_ONE} and ${PRIVATE_IP_TWO}" \
  serves_both_private_ips

echo "==> waiting for ${FALLTHROUGH_NAME} to still reach upstream DNS"
await_lookup "$FALLTHROUGH_NAME" \
  "${FALLTHROUGH_NAME} to fall through to upstream DNS" \
  resolved_upstream

echo "==> deleting one record and waiting for its address to stop being served"
kubectl -n "$TEST_NAMESPACE" delete privatednsrecord "${ZONE_NAME}-${RECORD_NAME}-one" >/dev/null
wait_for_zone_ready
await_lookup "$private_name" \
  "${PRIVATE_IP_ONE} to disappear while ${PRIVATE_IP_TWO} is still served" \
  stale_record_cleared

echo "private DNS smoke test passed"
