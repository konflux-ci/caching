---
name: updating-go-deps
description: Gotchas when updating Go dependencies
---

# Updating Go Dependencies

- `go.mod` has a **replace** directive for access-log-exporter (points to a fork)
- `tools.go` must import build/test-only deps — required for Cachi2 hermetic prefetch
- After changes: run `go mod tidy` then update lockfiles per [HERMETIC-BUILDS.md](../HERMETIC-BUILDS.md)
