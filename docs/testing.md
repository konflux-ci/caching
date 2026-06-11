# Testing Guide

This guide covers running and debugging tests for the caching proxy.

## Quick Start

```bash
# Full test suite via Helm (recommended)
mage all

# E2E tests with local debugging (mirrord)
mage test:cluster
```

> **Note**: `mage all` sets up the complete environment (cluster, images, deployment) and ends by invoking `helm test squid --timeout=15m`. It runs **unit tests** and **Helm-driven tests** — it does not run `mage test:cluster` (mirrord E2E).

## Running Tests via Helm

When running `helm test` directly (not through `mage all`), you must specify a timeout of at least 420 seconds:

```bash
helm test squid --timeout=420s
```

The default Helm test timeout is 5 minutes (300 seconds), which may not be sufficient for all tests to complete.

## Running Tests with Mirrord

For local development and debugging, use mirrord to run tests with cluster network access:

```bash
# Setup test environment first
mage squidHelm:up

# Run tests with cluster network access
mage test:cluster

# Run with multiple replicas
mage test:clusterMultiReplica
```

This uses mirrord to "steal" network connections from a target pod and runs the test locally (outside of the Kind cluster) with Ginkgo. This allows for local debugging without rebuilding test containers.

## Filtering Tests

Use the `GINKGO_LABEL_FILTER` environment variable to control which tests run:

```bash
# Skip tests with external dependencies (via Helm)
GINKGO_LABEL_FILTER='!external-deps' mage all

# Skip tests with external dependencies (via mirrord)
GINKGO_LABEL_FILTER='!external-deps' mage test:cluster
```

For syntax details, see the [Ginkgo label-filter documentation](https://onsi.github.io/ginkgo/#spec-labels).

## VS Code Integration

The repository includes complete VS Code configuration for Ginkgo testing.

### Debug Configurations

Use VS Code's debug panel (F5) to run tests with breakpoints:

| Configuration | Description |
|---------------|-------------|
| Debug Ginkgo E2E Tests | Run all E2E tests with debugging |
| Debug Ginkgo Tests (Current File) | Debug tests in the currently open file |
| Run Ginkgo Tests with Coverage | Generate test coverage reports |

### VS Code Tasks

Available via Ctrl+Shift+P → "Tasks: Run Task":

| Task | Description |
|------|-------------|
| Setup Test Environment | Runs `mage all` to prepare everything |
| Run Ginkgo E2E Tests | Run E2E tests locally with Ginkgo + mirrord |
| Run Ginkgo Tests (Current Directory) | Run tests in the currently open directory |
| Run Ginkgo Tests with Coverage | Generate test coverage reports |
| Run Focused Ginkgo Tests | Run specific test patterns |
| Check Test Environment Status | Run `mage kind:status && mage squidHelm:status` |
| Generate Ginkgo Test File | Bootstrap a new Ginkgo test file |
| Clean Test Environment | Clean up all resources |

## Contributing to Tests

When adding new tests:

1. **Use test helpers**: Leverage `tests/testhelpers/` for common operations
2. **Follow Ginkgo patterns**: Use `Describe`, `Context`, `It` for clear test structure
3. **Add cache-busting**: Use unique URLs to prevent test interference
4. **Verify cleanup**: Ensure tests clean up resources properly
5. **Update VS Code config**: Add debug configurations for new test files

## Mage Test Commands

| Command | Description |
|---------|-------------|
| `mage test:unit` | Run unit tests (no cluster required) |
| `mage test:cluster` | Run E2E tests with mirrord |
| `mage test:clusterMultiReplica` | Run E2E tests with multiple replicas |

For all available commands, run `mage -l`.
