# Changelog

## v1.0.1

### Added

- Helm lint, template, and package Makefile targets.
- CI validation for Helm chart linting and rendering.
- Release workflow chart packaging and GitHub release asset upload.

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
