---
name: local-dev-workflow
description: Use when setting up local development environment, understanding Mage target dependencies, or onboarding to the repo
---

# Local Development Workflow

## Overview

This repo uses **Mage** for automation. The quickest path to a working local environment is `mage all`.

## Quick Start

```bash
# One command to rule them all
mage all
```

This runs: `test:unit` → `squidHelm:up` → `helm test` → reset defaults

## Mage Target Dependencies

Understanding what depends on what:

```
mage all
├── test:unit (no cluster needed)
├── squidHelm:up
│   ├── build:loadSquid (depends on build:squid + kind:up)
│   ├── build:loadAccessLogExporter (depends on build:accessLogExporter + kind:up)
│   ├── build:loadTestImage (depends on build:testImage + kind:up)
│   └── helm upgrade --install (--wait --timeout=300s --debug)
├── helm test squid --timeout=15m
└── resetSquidToDefaults()
```

## Step-by-Step (Manual)

If you need more control:

```bash
# 1. Unit tests (no cluster)
mage test:unit

# 2. Create cluster
mage kind:up

# 3. Build and load images
mage build:squid
mage build:loadSquid
mage build:accessLogExporter
mage build:loadAccessLogExporter
mage build:testImage
mage build:loadTestImage

# 4. Deploy chart
mage squidHelm:up

# 5. Run e2e tests (via mirrord - different from mage all's helm test)
mage test:cluster
```

## Important Settings for Local Dev

Mage automatically sets these for local development:

```bash
--set environment=dev           # Use localhost images
--set nginx.enabled=true        # Enable nginx
--set test.labelFilter=...      # From GINKGO_LABEL_FILTER env
```

## Container Runtime

This repo uses **Podman**, not Docker:
- `mage build:*` calls `podman build`
- Image loading uses `podman save`

For devcontainer, ensure:
```bash
systemctl --user enable --now podman.socket
```

## Devcontainer Setup

The `.devcontainer/` provides a complete environment:

1. Install `podman-docker` package (Fedora) or symlink `docker → podman`
2. Start podman socket: `systemctl --user start podman.socket`
3. Open in VS Code → "Reopen in Container"

Prerequisites validated by `initialize.sh`:
- inotify limits
- Podman socket

## Cleanup

| Command | What it removes |
|---------|-----------------|
| `mage squidHelm:down` | Helm release + namespace deletion |
| `mage kind:down` | Kind cluster |
| `mage clean` | Cluster + squid/test images (NOT access-log-exporter) |

## Common Commands

```bash
mage -l                    # List all targets
mage kind:status           # Check cluster health
mage squidHelm:status      # Check deployment status
kubectl get pods -n caching # See running pods
```

## Success Indicators

After `mage all` succeeds, you should see:
- Cluster `caching` running
- Pods in namespace `caching`: squid, nginx, mirrord-test-target
- Squid accessible at `squid.caching.svc.cluster.local:3128`
- Nginx at port 80 (or 8080 depending on config)
