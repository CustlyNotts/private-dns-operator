#!/usr/bin/env bash
# Write the release version to every place it appears, so a tag push cannot fail
# the release workflow's consistency check and the docs cannot drift out of step
# with the artifacts they describe.
#
#   hack/set-version.sh v1.2.0           rewrite everything
#   hack/set-version.sh --check v1.2.0   report anything still naming another version
set -euo pipefail

CHECK=0
if [ "${1:-}" = "--check" ]; then
  CHECK=1
  shift
fi

TAG="${1:?usage: hack/set-version.sh [--check] vX.Y.Z}"
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like v1.2.3, got '$TAG'" >&2
  exit 1
fi
CHART_VERSION="${TAG#v}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CHART=charts/private-dns-operator
IMAGE=ghcr.io/custlynotts/private-dns-operator

# transform FILE  — rewrite FILE in place to name $TAG everywhere.
#
# Every versioned reference in the repository has to be listed here. A reference
# that is missing goes stale silently at the next release, which is exactly how
# the README ended up advertising a v1.0.1 image alongside a 1.1.0 chart.
transform() {
  local file="$1"
  sed -E \
    -e "s|^version: [0-9]+\.[0-9]+\.[0-9]+|version: ${CHART_VERSION}|" \
    -e "s|^appVersion: v[0-9]+\.[0-9]+\.[0-9]+|appVersion: ${TAG}|" \
    -e "s|^VERSION( +)\?= v[0-9]+\.[0-9]+\.[0-9]+|VERSION\1?= ${TAG}|" \
    -e "s|(${IMAGE}):v[0-9]+\.[0-9]+\.[0-9]+|\1:${TAG}|g" \
    -e "s|(--version) [0-9]+\.[0-9]+\.[0-9]+|\1 ${CHART_VERSION}|g" \
    -e "s|(private-dns-operator/)v[0-9]+\.[0-9]+\.[0-9]+/|\1${TAG}/|g" \
    -e "s|(current release \`)v[0-9]+\.[0-9]+\.[0-9]+(\`)|\1${TAG}\2|g" \
    "$file"
}

FILES=(
  "$CHART/Chart.yaml"
  "$CHART/README.md"
  Makefile
  README.md
  config/manager/manager.yaml
)

if [ "$CHECK" = "1" ]; then
  stale=()
  for file in "${FILES[@]}"; do
    if ! transform "$file" | diff -q - "$file" >/dev/null; then
      stale+=("$file")
    fi
  done
  if [ "${#stale[@]}" -gt 0 ]; then
    echo "these files do not consistently name ${TAG}:" >&2
    for file in "${stale[@]}"; do
      echo "  $file" >&2
      transform "$file" | diff "$file" - | sed -n '1,12p' | sed 's/^/      /' >&2
    done
    echo "run 'make set-version VERSION=${TAG}' and commit the result" >&2
    exit 1
  fi
  echo "all version references name ${TAG}"
  exit 0
fi

for file in "${FILES[@]}"; do
  transform "$file" > "$file.tmp" && mv "$file.tmp" "$file"
done

echo "set version to ${TAG} (chart ${CHART_VERSION}):"
git --no-pager diff --stat -- "${FILES[@]}"
echo
echo "Next: move the CHANGELOG 'Unreleased' heading to '## ${TAG}', commit, then"
echo "      git tag -a ${TAG} -m '${TAG}' && git push origin ${TAG}"
