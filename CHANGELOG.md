# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

Prepared for open-source release. The behaviour changes below are
backward-compatible with existing `PrivateDNSZone` and `PrivateDNSRecord`
objects, so this is a minor version.

### Added

- Apache License 2.0, `NOTICE`, and license headers on first-party Go files.
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, and `SECURITY.md` with the operator's
  threat model and private disclosure process.
- GitHub issue forms, a pull request template, `CODEOWNERS`, and Dependabot
  coverage for Go modules, GitHub Actions, and the base image.
- `PrivateDNSRecord` status: each record now reports the FQDN it resolves and an
  `Accepted` condition, with reasons `Accepted`, `ZoneNotFound`,
  `NamespaceNotAllowed`, `InvalidRecord`, and `ZoneNotReady`. A tenant can see
  why their record is not live without inspecting the zone.
- Last known good output is now held for `Forward` zones as well as `NXDOMAIN`
  zones. A zone whose desired state becomes invalid keeps its previously
  rendered Corefile stanza instead of dropping out of DNS.
- CRD schema validation generated from `+kubebuilder:validation` markers on the
  API types, so record type, TTL bounds, name lengths, and minimum value counts
  are rejected by the API server.
- `controller-gen` driven code generation with `make generate`, `make manifests`,
  `make sync-crds`, and a `make verify-codegen` check that fails if a
  regeneration was skipped.
- `make verify` runs the whole CI suite locally. `make help` lists every target.
- Rendered `install.yaml` published as a release asset, for
  `kubectl apply -f <release-url>` installs without a checkout.
- Image SBOMs, `mode=max` build provenance, and GitHub build attestations, so a
  published image can be verified with `gh attestation verify`.
- CI jobs for `gofmt`, `go vet`, `golangci-lint`, race-enabled tests with
  coverage, code generation drift, chart rendering, manifest schema validation
  with kubeconform, and a multi-platform image build on every pull request.
- Chart: a values reference README, `.helmignore`, an optional metrics `Service`
  and `ServiceMonitor` (both off by default, since the metrics endpoint is
  unauthenticated), Artifact Hub metadata, and `kubeVersion`.
- Test coverage for the reconcile loop, CoreDNS ConfigMap and Deployment
  patching, and DNS record validation.

### Changed

- The CoreDNS Corefile is now patched even when one zone fails to render.
  Previously a single invalid zone aborted the reconcile before the Corefile
  write, so no zone in the cluster received updates until it was fixed.
- ConfigMap key ownership is determined solely by the
  `dns.custlynotts.io/managed-zone-keys` annotation. The previous `.db` suffix
  heuristic meant a hand-maintained zone file in the CoreDNS ConfigMap could be
  unmounted from the Deployment's volume items.
- Zone status is written only when it actually changes, and condition
  `lastTransitionTime` is preserved across unchanged reconciles. Previously every
  reconcile stamped fresh timestamps, which re-triggered the watch that scheduled
  it.
- Claiming a zone with its finalizer and rendering it now happen in a single
  reconcile pass instead of requiring a requeue.
- Stale conditions are pruned, so `LastKnownGoodApplied` disappears once a zone
  recovers and `TemplateRendered`/`ZoneFileRendered` swap when the policy changes.
- `config/manager/manager.yaml` now sets a non-root `securityContext`, drops all
  capabilities, uses a read-only root filesystem, and declares resource requests
  and limits, matching the chart.
- `spec` is required on both CRDs, and `PrivateDNSZone` gained a `Records`
  printer column.
- Record validation error messages were reworded to lead with the value rather
  than the record type.

### Fixed

- The chart pinned `image.tag` in `values.yaml`, which overrode `appVersion`. A
  chart published at a new version would have deployed the `v1.0.1` image. The
  tag is now empty so `appVersion` drives it, and the release workflow fails if
  `Chart.yaml` and the git tag disagree.
- The `FQDN` printer column on `PrivateDNSRecord` was always empty, because no
  controller wrote the record's status.
- Zones could become permanently undeletable whenever the CoreDNS target was
  misconfigured. Finalizer release sat behind the Corefile read, so pointing the
  operator at a ConfigMap that did not exist, or one without a `Corefile` key,
  left every zone stuck terminating and blocked deletion of the CRD, even with
  the operator running. The operator now releases terminating zones when the
  target is provably absent, since it holds no state it could clean up, and
  reports `CoreDNSUnavailable` on live zones. A target that cannot be *read*, for
  example because RBAC does not cover it, still holds the finalizer, because the
  managed block may well still be live.
- A missing or unreadable CoreDNS ConfigMap produced an error with no status on
  any object. The failure is now reported on every live zone and its records.
- Declaring the same `CNAME` target more than once was rejected as a conflict.
  The check counted CNAME occurrences rather than distinct targets, so two
  `PrivateDNSRecord` objects naming the same target, or one record listing it
  twice, were treated the same as two records disagreeing about it. Identical
  `A` records were accepted in the same situation, which contradicted the
  documented support for compatible duplicates. The rule now asks whether any
  other type shares the name and how many *distinct* targets exist, normalising
  case and trailing dots, so duplicates that agree describe one RRset and are
  accepted. Genuine conflicts, a `CNAME` sharing a name with another type or
  resolving to more than one target, are still rejected, and the error is now
  attributed to the `CNAME` record rather than whichever record happened to sort
  first.
- Only the first of several records at the same name was ever served in `Forward`
  mode. Each source object rendered its own CoreDNS `template` stanza, and
  CoreDNS answers from the first stanza whose match expression hits, so a name
  backed by two `PrivateDNSRecord` objects resolved to one address. Records that
  share a name and type are now merged into a single stanza carrying every value,
  which is what the documented support for multiple `A` records always implied.
  Mixed TTLs within one RRset collapse to the lowest, per RFC 2181 section 5.2.
- Published `linux/arm64` images contained an x86-64 binary. The Dockerfile gave
  BuildKit's predefined `TARGETOS` and `TARGETARCH` args default values, which
  suppresses the values BuildKit injects, so every platform cross-compiled to
  `amd64`; the two architectures' binaries were byte-identical. The arm64 image
  would fail with `exec format error` on any node without binfmt emulation. The
  defaults are gone, and CI now extracts the binary from each platform's image
  and asserts its ELF machine type, because a green multi-platform build does not
  prove cross-compilation happened.
- A `PrivateDNSRecord` in a namespace that `allowedNamespaces` excludes froze the
  whole zone. The policy violation was reported as an invalid record set, so the
  zone went `Ready=False`, dropped its record count, and stopped publishing
  changes from permitted namespaces until the offending object was deleted. Any
  namespace explicitly *denied* access could therefore deny service to the zone,
  turning the delegation boundary into an attack on itself. Delegated records are
  now admitted individually: one that is barred or malformed is excluded and
  marked rejected on its own status while the zone carries on. The zone's own
  inline records still have to be valid, and a genuine conflict between admitted
  records, such as a `CNAME` sharing a name with other data, still holds the last
  known good output.
- `e2e/smoke-test.sh` failed against a healthy operator. It asserted DNS results
  immediately after the zone reported `Ready`, but that condition only means the
  CoreDNS ConfigMap has been written; the kubelet still has to re-project the
  volume and CoreDNS still has to reload. Measured convergence on a kind cluster
  was 50 seconds. Every DNS assertion now polls to a deadline, configurable with
  `DNS_TIMEOUT` and `DNS_POLL_INTERVAL`.
- CoreDNS writes could break the ConfigMap volume mount. CoreDNS mounts its
  ConfigMap with explicit volume `items`, so a key named in those items must
  exist in the ConfigMap and a file named by the Corefile must be mounted.
  Creating a zone needs the ConfigMap written first and retiring one needs the
  Deployment written first, but the operator always wrote the ConfigMap first.
  Retiring a zone could therefore leave volume items referencing a deleted key,
  stranding new CoreDNS pods in `FailedMount`, and creating one could leave the
  Corefile loading an unmounted file, crash-looping CoreDNS at startup. Both are
  cluster-wide DNS outages if a pod happens to start in the window, or if the
  reconcile fails persistently between the two writes.

  The commit is now sequenced to expand, switch, then contract: add the zone
  files, mount the union of the old and new key sets, switch the Corefile,
  unmount the retired keys, then delete them. Both conditions hold after every
  individual write, so an interrupted reconcile always leaves CoreDNS servable.
  The managed-keys annotation is widened before a key is created and narrowed
  only after one is deleted, so a partial reconcile never loses track of a key it
  still has to clean up. Each step is skipped when it would be a no-op, so a
  steady-state reconcile writes nothing and creating a zone costs three writes.

## v1.0.1

### Added

- Helm lint, template, and package Makefile targets.
- CI validation for Helm chart linting and rendering.
- Release workflow chart packaging and GitHub release asset upload.
- Publish Helm charts to GHCR as OCI artifacts with versions kept in lockstep with release tags.

### Fixed

- Use a repo-local Go build cache so CI runners do not depend on macOS-specific `/private/tmp`.

## v1.0.0

Initial release of `private-dns-operator`.

### Added

- `PrivateDNSZone` cluster-scoped API.
- `PrivateDNSRecord` namespaced API.
- CoreDNS managed-block reconciliation.
- `Forward` mode using CoreDNS `template` records with `fallthrough` and upstream `forward`.
- `NXDOMAIN` mode using CoreDNS `file` plugin for strict authoritative private zones.
- Compatible duplicate records for resilient DNS answers.
- CNAME conflict validation during reconcile.
- CoreDNS ConfigMap patching with managed-key tracking.
- CoreDNS Deployment volume item reconciliation.
- CoreDNS reload/rollout restart handling.
- CoreDNS target overrides through flags and environment variables.
- E2E smoke test script and guide.
- GitHub Actions CI and GHCR release workflow.
