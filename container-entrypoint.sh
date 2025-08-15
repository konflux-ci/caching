#!/bin/bash

set -euo pipefail

# Special mode: run standard squid-exporter as the only process (for sidecar use)
if [ "${1:-squid}" = "exporter" ]; then
  shift || true
  exec /usr/local/bin/squid-exporter "$@"
fi

# Configuration from environment variables (per-site exporter)
ENABLE_PER_SITE_EXPORTER="${ENABLE_PER_SITE_EXPORTER:-false}"
# Note: WEB_LISTEN_ADDRESS, WEB_TLS_CERT_FILE, WEB_TLS_KEY_FILE are read directly by the Go application

start_per_site_exporter() {
    # Build CLI flags from environment variables
    local args=()
    local listen_addr="${WEB_LISTEN_ADDRESS:-:9302}"
    args+=("-web.listen-address" "${listen_addr}")

    if [[ -n "${WEB_TLS_CERT_FILE:-}" && -n "${WEB_TLS_KEY_FILE:-}" ]]; then
        args+=("-web.tls-cert-file" "${WEB_TLS_CERT_FILE}")
        args+=("-web.tls-key-file" "${WEB_TLS_KEY_FILE}")
    fi

    exec /usr/local/bin/squid-per-site-exporter "${args[@]}"
}

start_squid() {
  echo "Initializing squid cache directory..."
  /usr/sbin/squid -f /etc/squid/squid.conf -z || true
  # Remove PID file created by -z initialization to avoid conflicts
  rm -f /run/squid/squid.pid || true

  echo "Starting squid proxy..."
  exec /usr/sbin/squid --foreground -f /etc/squid/squid.conf "$@"
}

# Main execution logic
if [[ "$ENABLE_PER_SITE_EXPORTER" == "true" ]]; then
  echo "Starting squid with per-site metrics exporter..."
  # Stream access logs to the exporter without modifying the main process
  ( tail -n +1 -F /var/log/squid/access_exporter.log | start_per_site_exporter ) &
  # Start squid with the stock configuration as PID 1
  exec /usr/sbin/squid --foreground -f /etc/squid/squid.conf "$@"
else
  echo "Starting squid without per-site exporter..."
  start_squid "$@"
fi
