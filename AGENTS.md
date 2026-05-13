# AGENTS.md

## Project Overview
Caching infrastructure for Konflux CI — deploys Squid forward proxy and Nginx reverse proxy to Kubernetes/OpenShift via a Helm chart. **The chart is in `squid/` but deploys both services** (tracked in KFLUXVNGD-827).

## Key Directories
- `cmd/` — Go sidecar binaries: `icap-server`, `squid-per-site-exporter`, `squid-store-id`
- `squid/` — Helm chart (templates, values, CRDs) for both Squid and Nginx
- `tests/e2e/` — Ginkgo E2E tests; `tests/helm/` — Helm template unit tests
- `.tekton/` — Konflux Pipelines-as-Code CI definitions
- `internal/` — Shared Go libraries (Helm, Kind cluster management)

## Build & Test (Mage, not Make)
```bash
mage -l                     # List all targets
mage all                    # Full setup: cluster + build + deploy + test
mage test:unit              # Unit tests (no cluster needed)
mage test:cluster           # E2E tests (requires kind + mirrord)
mage squidHelm:up           # Deploy/upgrade Helm chart
mage build:squid            # Build squid container image
```
Before committing: `mage test:unit && helm lint ./squid`

## Conventions
- Use **Podman**, not Docker — all Mage targets call `podman`
- Chart creates its own `caching` namespace — don't pass `helm -n caching`, the chart manages it
- E2E tests require `CGO_ENABLED=1` and mirrord installed
- Go tests use **Ginkgo/Gomega** BDD framework
- Filter tests: `GINKGO_LABEL_FILTER='!external-deps' mage test:cluster`

## Hermetic Builds (Cachi2)
All dependencies must be locked for network-isolated CI builds. When adding dependencies, update:
- `go.mod` / `go.sum` — Go modules (`go mod tidy`)
- `rpms.in.yaml` → regenerate `rpms.lock.yaml` — RPM packages
- `artifacts.lock.yaml` — Go/Helm toolchain versions
- `tools.go` — build-time Go tool imports
Forgetting lock files will break Konflux CI. See `HERMETIC-BUILDS.md`.

## Gotchas
- **Misleading names**: `squid/` chart deploys both squid AND nginx; `access-log-exporter` is for nginx, not squid
- **Squid image is multi-process**: runs squid, squid-exporter (:9301), per-site-exporter (:9302), ICAP (:1344)
- **`.tekton/` changes trigger CI** — edits here affect Pipelines-as-Code

## Workflow-Specific Gotchas
See `skills/` for detailed gotchas — load only what's relevant:
- `editing-helm-templates/` — StatefulSet naming, probe port changes
- `working-on-ci/` — image expiration, promotion location
- `updating-go-deps/` — replace directive, tools.go for Cachi2
