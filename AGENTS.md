# AGENTS.md

## Project Overview
Squid caching proxy for Konflux CI. Container images:
- `squid` — main proxy with SSL bump, ICAP, metrics
- `access-log-exporter` — Prometheus metrics exporter for nginx access logs
- `squid-tester` / `squid-test` — test image for e2e validation
- `squid-helm` — Helm chart OCI artifact

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

## Skills
See `skills/` for detailed guides on testing, debugging, Helm, Kind, and CI/CD.
