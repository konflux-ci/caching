---
name: kind-cluster-setup
description: Use when creating, managing, or troubleshooting the local kind cluster for development
---

# Kind Cluster Setup

## Overview

Local development uses a kind cluster. The cluster name **must be "caching"** (hardcoded in Mage).

## Quick Reference

| Task | Command |
|------|---------|
| Create/connect to cluster | `mage kind:up` |
| Check cluster status | `mage kind:status` |
| Delete cluster | `mage kind:down` |
| Force recreate (clean) | `mage kind:upClean` |

## Prerequisites (IMPORTANT)

**Increase inotify limits** before creating the cluster:

```bash
sudo sysctl fs.inotify.max_user_watches=1048576
sudo sysctl fs.inotify.max_user_instances=1024
```

Without this, kind will fail with "too many open files" errors.

**To make permanent**, add to `/etc/sysctl.conf`:
```
fs.inotify.max_user_watches=1048576
fs.inotify.max_user_instances=1024
```

## Cluster Details

| Property | Value |
|----------|-------|
| Cluster name | `caching` (hardcoded, not configurable) |
| Context name | `kind-caching` |
| Container runtime | Podman (Mage uses `podman` explicitly) |

## What `mage kind:up` Does

1. Checks if cluster "caching" exists
2. If not, runs `kind create cluster --name caching --wait 60s`
3. Exports kubeconfig: `kind export kubeconfig --name caching`

## Loading Images

After building images, load them into kind:

```bash
mage build:squid              # Build squid image
mage build:loadSquid          # Load into kind cluster
mage build:accessLogExporter  # Build sidecar image
mage build:loadAccessLogExporter
```

Mage uses `podman save` + `kind load image-archive` under the hood.

## Common Issues

**"Cluster already exists"**
- `mage kind:up` handles this gracefully (connects to existing)
- Use `mage kind:upClean` to force recreate

**"Cannot connect to cluster"**
```bash
# Re-export kubeconfig
kind export kubeconfig --name caching
kubectl cluster-info --context kind-caching
```

**Podman socket not running (devcontainer)**
```bash
systemctl --user start podman.socket
systemctl --user enable --now podman.socket
```

**"too many open files"**
- Increase inotify limits (see Prerequisites above)
- Restart kind: `mage kind:upClean`

## Cleanup

```bash
mage kind:down   # Delete cluster
mage clean       # Delete cluster + remove squid/test images (not access-log-exporter)
```

Note: `mage clean` does NOT remove the `access-log-exporter` image.
