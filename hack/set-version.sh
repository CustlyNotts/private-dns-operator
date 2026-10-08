#!/usr/bin/env bash
# Bump every place the release version is written down, so a tag push cannot
# fail the release workflow's consistency check.
#
#   hack/set-version.sh v1.1.0
set -euo pipefail

TAG="${1:?usage: hack/set-version.sh vX.Y.Z}"
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like v1.2.3, got '$TAG'" >&2
  exit 1
fi
CHART_VERSION="${TAG#v}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# BSD and GNU sed disagree on -i, so write through a temp file.
replace() {
  local pattern="$1" file="$2"
  sed -E "$pattern" "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

replace "s|^version: .*|version: ${CHART_VERSION}|" charts/private-dns-operator/Chart.yaml
replace "s|^appVersion: .*|appVersion: ${TAG}|" charts/private-dns-operator/Chart.yaml
replace "s|^VERSION( +)\?= .*|VERSION\1?= ${TAG}|" Makefile
replace "s|(ghcr\.io/custlynotts/private-dns-operator):v[0-9]+\.[0-9]+\.[0-9]+|\1:${TAG}|g" config/manager/manager.yaml
replace "s|(--version) [0-9]+\.[0-9]+\.[0-9]+|\1 ${CHART_VERSION}|g" README.md
replace "s|(--version) [0-9]+\.[0-9]+\.[0-9]+|\1 ${CHART_VERSION}|g" charts/private-dns-operator/README.md
replace "s|(private-dns-operator/)v[0-9]+\.[0-9]+\.[0-9]+/|\1${TAG}/|g" charts/private-dns-operator/README.md

echo "set version to ${TAG} (chart ${CHART_VERSION}):"
git --no-pager diff --stat -- charts Makefile config/manager README.md
echo
echo "Next: move the CHANGELOG 'Unreleased' heading to '## ${TAG}', commit, then"
echo "      git tag -a ${TAG} -m '${TAG}' && git push origin ${TAG}"
