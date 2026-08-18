#!/bin/bash
set -euo pipefail

# E2E Test Entrypoint
# This script prepares the environment and runs Ginkgo E2E tests

echo "=== Starting Ginkgo E2E Tests ==="
echo "Target namespace: ${TARGET_NAMESPACE:-caching}"
echo "Squid service: ${SQUID_SERVICE:-squid.caching.svc.cluster.local:3128}"
echo "Nginx service: ${NGINX_SERVICE:-nginx.caching.svc.cluster.local:8080}"

# The /app directory is read-only, so copy the chart to a writable temp directory.
# Chart archives must already be vendored into the image (see test.Containerfile).
CHART_DIR=$(mktemp -d)
echo "Copying chart to temp directory: $CHART_DIR"
cp -r /app/caching "$CHART_DIR/"

if [ ! -d "$CHART_DIR/caching/charts" ] || [ -z "$(ls -A "$CHART_DIR/caching/charts" 2>/dev/null || true)" ]; then
  echo "ERROR: Vendored helm chart dependencies missing under /app/caching/charts" >&2
  echo "ERROR: Rebuild the caching-tester image with hermetic chart prefetch (artifacts.lock.yaml)." >&2
  exit 1
fi

cd "$CHART_DIR"

echo "✓ Helm dependencies ready at: $CHART_DIR/caching"
ls -la ./caching/charts/

# Run the compiled test binary
echo "Running tests..."
exec /app/tests/e2e/e2e.test -ginkgo.v -ginkgo.label-filter="${LABEL_FILTER:-}"
