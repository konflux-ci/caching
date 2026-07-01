# AGENTS.md

Keep this file concise for agents (CI limit: 300 lines). Detailed guides live in `docs/` and `skills/`.

## Project Overview
Caching infrastructure for Konflux CI — deploys Squid forward proxy and Nginx reverse proxy to Kubernetes/OpenShift via a Helm chart. The chart is in `caching/` and deploys both services.

## Key Directories
- `cmd/` — Go sidecar binaries: `icap-server`, `squid-per-site-exporter`, `squid-store-id`
- `caching/` — Helm chart (templates, values, CRDs) for both Squid and Nginx
- `tests/e2e/` — Ginkgo E2E tests; `tests/helm/` — Helm template unit tests
- `.tekton/` — Konflux Pipelines-as-Code CI definitions
- `internal/` — Shared Go libraries (Helm, Kind cluster management)

## Build & Test (Mage)
```bash
mage -l                     # List all targets
mage all                    # Full setup: cluster + build + deploy + test
mage test:unit              # Unit tests (no cluster needed)
mage test:cluster           # E2E tests (requires kind + mirrord)
mage cachingHelm:up         # Deploy/upgrade Helm chart
mage build:squid            # Build squid container image
mage lint:go                # golangci-lint (see Linting below)
```
Before committing: `mage test:unit` (helm, hadolint, golangci, and shellcheck run in CI — see Linting).
**Note**: Mage version is derived dynamically from `go.mod` via `go install "github.com/magefile/mage@$(go list -m -f '{{.Version}}' github.com/magefile/mage)"`.

## Linting
Local and CI use dedicated checks; most are **not** run inside the devcontainer job anymore.

| Check | Local | CI workflow |
|-------|-------|-------------|
| golangci-lint | `mage lint:go` | `go-lint.yaml` |
| Helm chart | `helm lint ./caching` | `helm-lint.yaml` |
| Containerfiles | — | `hadolint.yaml` |
| Shell scripts (diff) | — | `differential-shellcheck.yaml` |

- golangci-lint v2 — version pinned in `.golangci-lint-version`; config in `.golangci.yml`

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
- **Misleading name**: `access-log-exporter` is for nginx, not squid
- **Chart directory vs release name**: the Helm chart directory is `caching/` but the default Helm release name remains `squid`
- **Squid image is multi-process**: runs squid, squid-exporter (:9301), per-site-exporter (:9302), ICAP (:1344)
- **`.tekton/` changes trigger CI** — edits here affect Pipelines-as-Code

## Workflow-Specific Gotchas
See `skills/` for detailed gotchas — load only what's relevant:
- `editing-helm-templates/` — StatefulSet naming, probe port changes
- `working-on-ci/` — image expiration, promotion location
- `updating-go-deps/` — replace directive, tools.go for Cachi2
