# AGENTS.md

## Project Overview
Caching infrastructure for Konflux CI. **Note:** The chart is named `squid/` but deploys **multiple services** (squid proxy AND nginx reverse proxy) — this is tech debt being tracked in KFLUXVNGD-827.

Container images:
- `squid` — main caching proxy with SSL bump, ICAP, metrics (this one IS squid-only)
- `access-log-exporter` — Prometheus metrics exporter (used with **nginx**, not squid)
- `squid-tester` / `squid-test` — test image for e2e validation of **both** squid and nginx (misleading name)
- `squid-helm` — Helm chart OCI artifact (deploys both services)

Deployed via Helm chart in `squid/` to OpenShift/Kubernetes.

## Setup Commands
- Install Mage: `go install github.com/magefile/mage@v1.16.1`
- List targets: `mage -l`
- Full local setup: `mage all`
- Unit tests only: `mage test:unit`

## Code Style
- Go with standard formatting (`gofmt`)
- Helm chart in `squid/` — lint with `helm lint ./squid`
- Main Containerfile uses multi-stage builds

## Testing Instructions
- Unit tests: `mage test:unit` (no cluster needed)
- E2E tests: `mage test:cluster` (requires kind + mirrord)
- Before committing: `mage test:unit && helm lint ./squid`

## Important Conventions
- Use **Podman**, not Docker — all Mage targets call `podman`
- Chart creates its own `caching` namespace — avoid `helm -n` for app namespace
- E2E tests require `CGO_ENABLED=1` and mirrord installed
- `.tekton/` is Konflux Pipelines-as-Code — changes trigger CI

## General Gotchas
- **Misleading chart name**: `squid/` chart deploys both squid AND nginx services
- **`access-log-exporter`** is for nginx logs, not squid logs (despite being in this repo)

## Workflow-Specific Gotchas
See `skills/` for gotchas NOT covered in README — load only what's relevant:
- `editing-helm-templates/` — StatefulSet naming, probe port changes
- `working-on-ci/` — image expiration, promotion location
- `updating-go-deps/` — replace directive, tools.go for Cachi2
