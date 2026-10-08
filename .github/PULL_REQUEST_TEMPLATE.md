## What this changes

<!-- A short description, and the issue it closes if there is one. -->

Closes #

## Why

<!-- The problem this solves. Skip if it is obvious from the description. -->

## Checklist

- [ ] `make verify` passes locally
- [ ] Tests added or updated for the changed behaviour
- [ ] `make generate manifests sync-crds` run and the regenerated files committed, if API types changed
- [ ] `CHANGELOG.md` updated under `## Unreleased`
- [ ] `README.md` updated, if flags, values, or user-visible behaviour changed
- [ ] Chart values table updated, if a new Helm value was added
- [ ] Commits signed off with `git commit -s`

## CoreDNS impact

<!--
Does this change what the operator writes to the CoreDNS ConfigMap or
Deployment? If so, paste a before/after of the managed Corefile block.
Write "none" if it does not.
-->
