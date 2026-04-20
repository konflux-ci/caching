---
name: debugging-squid
description: Use when debugging squid proxy issues, checking logs, or troubleshooting deployment problems
---

# Debugging Squid

## Overview

Squid runs as a **StatefulSet** (not Deployment) in namespace `caching`. Multiple sidecars provide metrics and ICAP.

## Quick Diagnostics

```bash
# Check pod status
kubectl get pods -n caching

# Check StatefulSet (not deployment!)
kubectl get sts -n caching

# Get squid pod logs
kubectl logs -n caching -l app.kubernetes.io/name=squid -c squid

# Get all container logs from squid pod
kubectl logs -n caching -l app.kubernetes.io/name=squid --all-containers
```

## Pod Components

The squid pod has multiple containers:

| Container | Port | Purpose |
|-----------|------|---------|
| `squid` | 3128, 9302 | Main proxy + per-site exporter (when enabled) |
| `squid-exporter` | 9301 | Prometheus metrics |
| `icap-server` | 1344 | ICAP service |

**Note:** Per-site exporter runs INSIDE the `squid` container (not a separate container). Use `-c squid` for its logs.

## Normal vs Abnormal Logs

### This is NORMAL (don't panic):
```
error:transaction-end-before-headers
```
This can appear when **TCP clients** connect and disconnect without sending HTTP headers. With default settings (`perSiteExporter.enabled: true`), squid probes use HTTPS on 9302, not TCP—so this log often comes from other TCP clients, not kubelet probes.

### Actually concerning:
- SSL certificate errors
- "Connection refused" to upstream
- OOM kills
- CrashLoopBackOff

## Checking Specific Containers

```bash
# Squid proxy logs (includes per-site exporter output)
kubectl logs -n caching <pod-name> -c squid

# Metrics exporter
kubectl logs -n caching <pod-name> -c squid-exporter

# ICAP server
kubectl logs -n caching <pod-name> -c icap-server
```

## Health Check Endpoints

| Endpoint | What it checks |
|----------|----------------|
| HTTPS 9302 /health | Squid container (default, when perSiteExporter enabled) |
| TCP 3128 | Squid listening (only when perSiteExporter disabled) |
| HTTP 9301 /metrics | squid-exporter container |
| TCP 1344 | ICAP server container |

**Note:** With default values (`perSiteExporter.enabled: true`), squid probes use HTTPS /health on 9302, NOT TCP 3128.

## Testing Proxy Manually

```bash
# From inside cluster (or via mirrord)
curl -x http://squid.caching.svc.cluster.local:3128 http://example.com

# Check if squid is accepting connections
kubectl exec -n caching <squid-pod> -c squid -- squidclient -h localhost mgr:info
```

## Common Issues

**Pods stuck in Pending**
```bash
kubectl describe pod -n caching <pod-name>
# Check events for scheduling issues
```

**Pods in CrashLoopBackOff**
```bash
kubectl logs -n caching <pod-name> -c squid --previous
# Check previous container logs
```

**SSL bump errors**
- Check cert-manager is working: `kubectl get certificates -n caching`
- Check trust bundle: `kubectl get configmap -n caching`

**Cache not working**
- Check cache directories mounted
- Check squid.conf for cache_dir settings
- Verify storage class if using PVCs

## Using mirrord for Local Debugging

Run local code with cluster network access:

```bash
mirrord exec --config-file .mirrord/mirrord.json -- go run ./cmd/...
```

This steals traffic to `mirrord-test-target` pod, letting you debug locally while connected to cluster services.

## Helm Test Failures

When `mage all` or `helm test` fails:
```bash
kubectl logs -n caching squid-test
```

The test pod runs ginkgo tests; logs show which tests failed.
