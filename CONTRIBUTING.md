# Contributing to private-dns-operator

Thanks for your interest in improving `private-dns-operator`. This document
covers the local toolchain, the code generation rules, and what CI expects from
a pull request.

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Prerequisites

| Tool | Version | Needed for |
| ---- | ------- | ---------- |
| Go | matches `go.mod` (currently 1.26) | build and test |
| `controller-gen` | v0.19.0 | CRD and deepcopy generation |
| `golangci-lint` | v2.6.0 | linting |
| Helm | 3.14+ | chart lint, template, package |
| `kustomize` | 5.x | rendering `config/` |
| Docker | any recent | image builds |
| `kubectl` + a cluster with CoreDNS | — | the e2e smoke test |

`make controller-gen`, `make golangci-lint`, and `make kustomize` download
pinned binaries into `bin/`, so you only need Go on the path to get started.

## Build and Test

```bash
make test          # go test ./... with the race detector
make build         # builds bin/manager
make lint          # golangci-lint run
make fmt vet       # gofmt and go vet
make verify        # everything CI runs, in one target
```

Run `make verify` before opening a pull request. It is the same set of checks the
CI workflow runs, so a green `make verify` means a green CI in all but
environment-specific cases.

## Generated Code and Manifests

**`api/v1alpha1/zz_generated.deepcopy.go` and `config/crd/bases/*.yaml` are
generated. Never hand-edit them.**

Validation lives on the Go types as `+kubebuilder:validation:*` markers, and
`controller-gen` turns those into the CRD OpenAPI schema. If you add a field:

1. Add the field to the type in `api/v1alpha1/` **with its validation markers**.
2. Run `make generate manifests` to regenerate deepcopy functions and CRDs.
3. Run `make sync-crds` to copy the CRDs into `charts/private-dns-operator/crds/`.
4. Commit the regenerated files alongside your change.

`make verify` fails if any generated file is out of date, so CI catches a
forgotten regeneration. There is also no second source of truth for validation:
if a constraint is not expressed as a marker on the type, it does not exist.

`api/v1alpha1/crd_samples_test.go` validates every manifest in `config/samples`
against the generated CRD schema, using the same validator the API server uses.
Add a sample for a new field, and it will be checked.

## Chart Changes

The chart under `charts/private-dns-operator/` is released in lockstep with the
operator image. A git tag `vX.Y.Z` produces image tag `vX.Y.Z` and chart version
`X.Y.Z`.

- Do not pin `image.tag` in `values.yaml`. Leave it empty so the chart's
  `appVersion` drives the image tag, and the release workflow can set both.
- `charts/private-dns-operator/crds/` is a generated copy. Use `make sync-crds`.
- Any new value needs a row in the chart
  [README](charts/private-dns-operator/README.md) values table.
- Run `make helm-lint helm-template` locally.

## Pull Requests

- Branch from `main`.
- Keep one logical change per pull request.
- Add or update tests. Pure rendering logic belongs in `internal/dns` or
  `internal/coredns` with table-driven tests; reconcile behaviour belongs in
  `internal/controller` using the controller-runtime fake client.
- Update `CHANGELOG.md` under an `## Unreleased` heading.
- Update the `README.md` if you change user-visible behaviour, flags, or values.
- Commits are signed off under the [Developer Certificate of
  Origin](https://developercertificate.org/). Use `git commit -s`, which adds a
  `Signed-off-by` trailer asserting you have the right to submit the code.

## Commit Messages

Write the subject line in the imperative mood, under 72 characters, with no
trailing period:

```text
Keep healthy zones rendering when one zone is invalid
```

Explain the *why* in the body when the change is not self-evident.

## Testing Against a Real Cluster

Unit tests cover rendering and reconcile logic without a cluster. Anything that
touches real CoreDNS behaviour should also be exercised with the smoke test:

```bash
make docker-build VERSION=v0.0.0-dev
# load the image into your cluster, install the chart pointing at it, then:
./e2e/smoke-test.sh
```

See [docs/e2e.md](docs/e2e.md) for the overrides the script accepts and what it
proves.

**Be careful where you run it.** The operator rewrites the CoreDNS ConfigMap in
the target namespace. Use a disposable cluster (kind, k3d, a scratch namespace
with its own CoreDNS) and never point a development build at a cluster you care
about.

## Cutting a Release

The release version is written down in several places, and the release workflow
refuses to publish a tag that disagrees with `Chart.yaml`. One target keeps them
in step:

```bash
make set-version VERSION=v1.1.0
```

That updates `Chart.yaml` (`version` and `appVersion`), the `Makefile` default,
the image tag in `config/manager/manager.yaml`, and the version references in
both READMEs. Then:

1. Move the `## Unreleased` heading in `CHANGELOG.md` to `## v1.1.0`.
2. Run `make release-check VERSION=v1.1.0`. It runs the full verification suite
   and confirms `Chart.yaml` matches the version you are about to tag.
3. Commit the result and merge it to `main`.
4. Tag and push:

   ```bash
   git tag -a v1.1.0 -m 'v1.1.0'
   git push origin v1.1.0
   ```

The tag triggers `.github/workflows/release.yaml`, which re-runs `make verify`
before publishing anything, then pushes the image and chart to GHCR, attaches
`install.yaml` and the packaged chart to a GitHub release, and records build
provenance.

Pick the version with [Semantic Versioning](https://semver.org/) in mind, and
remember that the CRD API version (`v1alpha1`) is separate from the release
version. A breaking change to the CRD schema needs a new API version, not just a
major release.

## Reporting Bugs and Requesting Features

Use the [issue templates](https://github.com/CustlyNotts/private-dns-operator/issues/new/choose).
For anything security-sensitive, follow [SECURITY.md](SECURITY.md) instead of
opening a public issue.

## License

Contributions are accepted under the [Apache License 2.0](LICENSE), the same
license that covers the project.
