---
name: updating-go-deps
description: Gotchas when updating Go dependencies
---

# Updating Go Dependencies

- `tools.go` must import build/test-only deps — required for Cachi2 hermetic prefetch
- After changes: run `go mod tidy` then update lockfiles per [HERMETIC-BUILDS.md](../../HERMETIC-BUILDS.md)
- Renovate handles direct Go module updates automatically. Indirect deps are excluded from Renovate (see `.github/renovate.json` packageRules) — they update transitively via `go mod tidy`
