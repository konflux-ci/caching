---
name: helm-chart-development
description: Use when modifying the Helm chart, testing chart changes, or troubleshooting deployment issues
---

# Helm Chart Development

## Overview

The Squid Helm chart is in `squid/`. Key quirk: the chart **creates its own namespace** - do NOT use `helm --namespace` or `-n` with this chart.

## Important Facts

| Fact | Detail |
|------|--------|
| Chart location | `squid/` |
| Workload type | **StatefulSet** (not Deployment, despite filename `deployment.yaml`) |
| Namespace | Chart creates namespace (default `caching` via `namespace.name`, overridable) |
| nginx | **Disabled by default** - Mage enables it |

## Environment-Based Image Selection

The chart uses `environment` value to select images:

| Environment | Image Registry |
|-------------|----------------|
| `dev` (local) | `localhost/konflux-ci/...` |
| `prerelease` | `quay.io/redhat-user-workloads/konflux-vanguard-tenant/...` |
| `release` (default) | `quay.io/konflux-ci/caching/...` |

For local development:
```bash
helm upgrade --install squid ./squid --set environment=dev --set nginx.enabled=true
```

## Common Value Overrides

```bash
# Local development
--set environment=dev
--set nginx.enabled=true

# Skip cert-manager (if already installed)
--set installCertManagerComponents=false
--set selfsigned-bundle.enabled=false

# Multi-replica testing
--set replicaCount=3

# Test label filter
--set test.labelFilter='!external-deps'
```

## Validating Changes

1. **Lint the chart:**
   ```bash
   helm lint ./squid
   ```

2. **Update dependencies (if Chart.yaml changed):**
   ```bash
   helm dependency build ./squid
   ```

3. **Run template tests:**
   ```bash
   mage test:unitHelmTemplate
   ```

4. **Deploy and test:**
   ```bash
   mage squidHelm:up
   mage test:cluster
   # Or full helm test (needs ≥420s timeout):
   helm test squid --timeout=15m
   ```

## Chart Dependencies

- **cert-manager** (jetstack)
- **trust-manager** (jetstack)

Controlled by `installCertManagerComponents` value.

## Key Templates

| Template | Creates |
|----------|---------|
| `deployment.yaml` | Squid **StatefulSet** (misleading name) |
| `nginx-statefulset.yaml` | Nginx StatefulSet (when enabled) |
| `mirrord-target-pod.yaml` | mirrord test target pod |
| `test-pod.yaml` | Helm test hook pod (`squid-test`) |

## Timeouts

| Context | Timeout |
|---------|---------|
| `mage squidHelm:up` | 300s (5 min) |
| `mage all` helm test | 15m |
| Raw `helm test` | Need ≥420s (7 min) |

## Common Issues

**"namespace caching not found"**
The chart creates it. If missing, redeploy:
```bash
mage squidHelm:up
```

**StatefulSet vs Deployment confusion**
It's a StatefulSet. Use:
```bash
kubectl get sts -n caching
kubectl get pods -n caching
```

**nginx not deploying**
nginx is disabled by default:
```bash
--set nginx.enabled=true
```
