---
name: dependency-automation
description: Renovate auto-merge, grouping, and custom manager configuration
---

# Dependency Automation (Renovate)

Configuration lives in `.github/renovate.json`. Renovate runs via Mend integration on GitHub.

## Global Settings

- `automergeStrategy`: `merge-commit` (not squash/rebase)
- `platformAutomerge`: `true` — uses GitHub's merge queue when available
- `lockFileMaintenance`: enabled but does **not** auto-merge — PRs require manual merge (see #945)

## Package Rules

| Rule | Scope | Auto-merge | Grouped |
|------|-------|------------|---------|
| Indirect Go deps | `gomod` indirect | Disabled entirely | — |
| cert-manager | All cert-manager packages and images | Yes | `cert-manager` |
| Konflux components | `quay.io/konflux-ci/*` patch/digest | Yes | No |
| Tekton bundles | `quay.io/konflux-ci/tekton-catalog/*` | Yes | `tekton-bundles` |
| Container base images | Red Hat/Fedora registries patch/digest | Yes | `container-images` |
| Go toolchain | `golang-version` + `custom.golang-with-checksum` patch | Yes | `go-toolchain` |

- Indirect Go deps are excluded because they are resolved transitively by `go mod tidy` when direct deps change
- All auto-merged rules also set `autoApprove: true`

## Custom Managers

Three regex-based custom managers track versions that Renovate's built-in managers do not detect:

1. **golangci-lint version pin** — tracks the version in `.golangci-lint-version` against `golangci/golangci-lint` GitHub releases
2. **Helm version in Tekton pipelines** — tracks the `helm-version` parameter default in `.tekton/*.yaml` against `helm/helm` GitHub releases
3. **Go version+SHA256 in devcontainer** — tracks `GO_VERSION` and `GO_SHA256` ARGs in `.devcontainer/Containerfile` using the custom `golang-with-checksum` datasource

## Custom Datasource

`golang-with-checksum` fetches the official Go release list from `go.dev/dl/` and extracts the version and SHA256 digest for stable `linux/amd64` archives. This enables Renovate to update both the Go version and its checksum in the devcontainer Containerfile atomically.

## What Requires Manual Intervention

- Adding new system packages (RPMs) — see `HERMETIC-BUILDS.md`
- Adding new build-time Go tools — see `skills/updating-go-deps/`
- Lock file maintenance PRs — currently require manual merge
- Any dependency not covered by the rules above
