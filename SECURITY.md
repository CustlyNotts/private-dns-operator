# Security Policy

## Supported Versions

Security fixes are applied to the latest released minor version. Older tags do
not receive backports while the project is pre-1.x in API maturity terms.

| Version | Supported |
| ------- | --------- |
| 1.0.x   | yes       |
| < 1.0   | no        |

## Reporting a Vulnerability

Please do **not** open a public GitHub issue for security problems.

Report privately through
[GitHub Security Advisories](https://github.com/CustlyNotts/private-dns-operator/security/advisories/new),
or email <kehinde.aturuka@linux.com> with `private-dns-operator` in the subject.

Include where practical:

- the affected version or image tag
- the Kubernetes and CoreDNS versions
- a minimal `PrivateDNSZone` / `PrivateDNSRecord` manifest that reproduces it
- the resulting Corefile or ConfigMap output
- the impact you believe it has

You can expect an acknowledgement within 5 working days and a status update at
least every 10 working days until the report is resolved. Please allow 90 days
for a fix before public disclosure.

## Threat Model

This operator holds `get`, `update`, and `patch` on the CoreDNS ConfigMap and
Deployment in the CoreDNS namespace. That is the privilege that matters:

- **Cluster-wide DNS control.** Anything able to create or mutate a
  `PrivateDNSZone` can change what names resolve to for every pod in the
  cluster. Treat `PrivateDNSZone` as a cluster-admin-level resource and restrict
  it with RBAC accordingly.
- **Delegated record creation.** `PrivateDNSRecord` is namespaced and intended
  for delegation. A zone that leaves `spec.allowedNamespaces.matchNames` empty
  accepts records from **every** namespace. Set `matchNames` explicitly on any
  zone where tenants should not be able to publish names.
- **Blast radius containment.** The operator writes only between the
  `# BEGIN private-dns-zone-operator` and `# END private-dns-zone-operator`
  markers in the Corefile, and only to ConfigMap keys it tracks in the
  `dns.custlynotts.io/managed-zone-keys` annotation. Reports of writes outside
  that boundary are treated as security issues, not bugs.
- **Out of scope.** Misconfiguration of an unrelated CoreDNS plugin, upstream
  CoreDNS vulnerabilities, and privilege granted deliberately through your own
  RBAC are not vulnerabilities in this project.

## Hardening Recommendations

- Restrict `create`, `update`, and `patch` on `privatednszones` to platform
  administrators.
- Set `spec.allowedNamespaces.matchNames` on every zone exposed to tenants.
- Run the operator with the shipped `securityContext` defaults: non-root,
  read-only root filesystem, all capabilities dropped.
- Keep the CoreDNS RBAC `Role` scoped with `resourceNames` to exactly the
  ConfigMap and Deployment you intend the operator to manage.
