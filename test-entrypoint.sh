#!/bin/bash
set -euo pipefail

# E2E Test Entrypoint
# This script prepares the environment and runs Ginkgo E2E tests

echo "=== Starting Ginkgo E2E Tests ==="
echo "Target namespace: ${TARGET_NAMESPACE:-caching}"
echo "Squid service: ${SQUID_SERVICE:-squid.caching.svc.cluster.local:3128}"
echo "Nginx service: ${NGINX_SERVICE:-nginx.caching.svc.cluster.local:8080}"

# Retry wrapper for transient cluster DNS/egress failures (e.g. CoreDNS
# "connection refused" when fetching charts.jetstack.io).
retry_cmd() {
  local description="$1"
  shift
  local max_attempts="${HELM_FETCH_MAX_ATTEMPTS:-8}"
  local delay="${HELM_FETCH_INITIAL_DELAY_SEC:-2}"
  local max_delay="${HELM_FETCH_MAX_DELAY_SEC:-30}"
  local attempt=1
  local rc=0

  while (( attempt <= max_attempts )); do
    echo "→ ${description} (attempt ${attempt}/${max_attempts})"
    # Capture status immediately: after `if cmd; then ...; fi`, $? is 0 even on failure.
    "$@" && return 0
    rc=$?
    if (( attempt == max_attempts )); then
      break
    fi
    echo "WARNING: ${description} failed with exit ${rc}; retrying in ${delay}s..." >&2
    sleep "${delay}"
    delay=$(( delay * 2 ))
    if (( delay > max_delay )); then
      delay=${max_delay}
    fi
    attempt=$(( attempt + 1 ))
  done

  echo "ERROR: ${description} failed after ${max_attempts} attempts" >&2
  return "${rc}"
}

# The /app directory is read-only, so copy the chart to a writable temp directory.
CHART_DIR=$(mktemp -d)
echo "Copying chart to temp directory: $CHART_DIR"
cp -r /app/caching "$CHART_DIR/"

echo "Building helm chart dependencies from charts.jetstack.io..."
# Preflight reaches the chart index over HTTPS so DNS/egress flakes fail fast/clearly.
retry_cmd "fetch jetstack chart index" \
  curl -fsS --connect-timeout 5 --retry 0 --max-time 60 \
  -o /dev/null "https://charts.jetstack.io/index.yaml"
retry_cmd "helm repo add jetstack" \
  helm repo add jetstack https://charts.jetstack.io --force-update
retry_cmd "helm repo update jetstack" \
  helm repo update jetstack
retry_cmd "helm dependency build" \
  helm dependency build "$CHART_DIR/caching"

cd "$CHART_DIR"

echo "✓ Helm dependencies ready at: $CHART_DIR/caching"
ls -la ./caching/charts/

# Run the compiled test binary
echo "Running tests..."
exec /app/tests/e2e/e2e.test -ginkgo.v -ginkgo.label-filter="${LABEL_FILTER:-}"
