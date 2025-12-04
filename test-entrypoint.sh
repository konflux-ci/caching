#!/bin/bash
set -euo pipefail

# E2E Test Entrypoint
# This script prepares the environment and runs Ginkgo E2E tests

echo "=== Starting Ginkgo E2E Tests ==="
echo "Target namespace: ${TARGET_NAMESPACE:-caching}"
echo "Squid service: ${SQUID_SERVICE:-squid.caching.svc.cluster.local:3128}"

# Build helm chart dependencies in a writable temp directory
# The /app directory is read-only, so we need to copy the chart to /tmp
echo "Building helm chart dependencies..."
helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
helm repo update

# Copy chart to writable temp directory
CHART_DIR=$(mktemp -d)
echo "Copying chart to temp directory: $CHART_DIR"
cp -r /app/squid "$CHART_DIR/"

# Build dependencies in the temp directory
if ! helm dependency build "$CHART_DIR/squid"; then
  echo "ERROR: Failed to build helm dependencies"
  exit 1
fi

# Verify dependencies were downloaded
if [ ! -d "$CHART_DIR/squid/charts" ]; then
  echo "ERROR: Charts directory not created"
  exit 1
fi

echo "✓ Helm dependencies ready at: $CHART_DIR/squid"
ls -la "$CHART_DIR/squid/charts/"

# Change to the temp directory so tests use the chart with dependencies
cd "$CHART_DIR"

echo "✓ Changed to temp directory: $CHART_DIR"
ls -la ./squid/charts/

# Run the compiled test binary
echo "Running tests..."
exec /app/tests/e2e/e2e.test -ginkgo.v -ginkgo.label-filter="${LABEL_FILTER:-}"

