---
name: running-tests
description: Use when running unit tests, e2e tests, checking coverage, or troubleshooting test failures
---

# Running Tests

## Overview

This repo uses **Mage** for test automation. Unit tests don't require a cluster; e2e tests require kind + mirrord.

## Quick Reference

| Task | Command | Requires Cluster |
|------|---------|------------------|
| All unit tests + coverage | `mage test:unit` | No |
| Per-site exporter only | `mage test:unitExporter` | No |
| Store ID helper only | `mage test:unitStoreID` | No |
| ICAP server only | `mage test:unitICAPServer` | No |
| Helm template tests | `mage test:unitHelmTemplate` | No |
| E2E tests (1 replica) | `mage test:cluster` | Yes |
| E2E tests (3 replicas) | `mage test:clusterMultiReplica` | Yes |
| Unit + deploy + in-cluster helm test | `mage all` | Yes |

## Unit Tests

No cluster needed. Just run:

```bash
mage test:unit
```

Coverage outputs to `coverage.out`:
```bash
go tool cover -html=coverage.out  # View in browser
```

**Helm template tests** need dependencies first (done automatically in BeforeSuite):
```bash
helm repo add jetstack https://charts.jetstack.io
helm dependency build ./squid
```

## E2E Tests

### Two Different E2E Paths

| Command | Test method | Uses mirrord? |
|---------|-------------|---------------|
| `mage all` | In-cluster helm test pod (`squid-test`) | No |
| `mage test:cluster` | Local ginkgo binary via mirrord | Yes |

Both use `GINKGO_LABEL_FILTER` but run tests differently.

### Prerequisites (for `mage test:cluster`)

- [ ] Kind cluster running (`mage kind:up`)
- [ ] Images built and loaded (`mage squidHelm:up` does this)
- [ ] mirrord installed and in PATH
- [ ] Pod `mirrord-test-target` Ready in namespace `caching`

### How It Works

1. Mage builds ginkgo binary with `CGO_ENABLED=1`
2. Runs via mirrord: `mirrord exec --config-file .mirrord/mirrord.json -- ./tests/e2e/e2e.test`
3. Tests run as if inside the cluster

### Running E2E

```bash
# After cluster is ready
mage test:cluster

# Or skip network-dependent tests
GINKGO_LABEL_FILTER='!external-deps' mage test:cluster
```

## Ginkgo Labels

| Label | Meaning |
|-------|---------|
| `external-deps` | Requires network (container pulls) |
| `nginx` | Nginx-specific tests |
| `monitoring` | NGINX access-log exporter tests only (nginx_access_log_*.go) |

Filter example:
```bash
GINKGO_LABEL_FILTER='!external-deps && !nginx' mage test:cluster
```

## Common Issues

**"mage: command not found"**
```bash
go install github.com/magefile/mage@latest
```

**"mirrord not found"**
```bash
curl -fsSL https://raw.githubusercontent.com/metalbear-co/mirrord/main/scripts/install.sh | bash
```

**"mirrord target pod not ready"**
```bash
kubectl get pods -n caching  # Check mirrord-test-target exists
mage squidHelm:up            # Redeploy if missing
```

**E2E tests timeout**
- Check cluster: `kubectl cluster-info --context kind-caching`
- Try with filter: `GINKGO_LABEL_FILTER='!external-deps' mage test:cluster`

**Helm template tests fail**
```bash
helm dependency build ./squid
```
