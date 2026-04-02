#!/bin/bash
set -o pipefail

# When a child process exits, bash sends SIGCHLD. By default, bash ignores
# this signal and blocking builtins like `wait` continue sleeping. Setting a
# trap (even a no-op like `true`) causes `wait` to return immediately when
# SIGCHLD arrives, so the script can react to a child crash without delay.
trap true CHLD

SPOOL_DIR="/var/spool/squid"
SSL_DB_DIR="${SPOOL_DIR}/ssl/db"
CACHE_DIR="${SPOOL_DIR}/cache"
CERT_FILE="/etc/squid/certs/tls.crt"

# Clean cache if disk usage exceeds threshold to handle cases where Squid
# crashed with a full cache.
# Follows Squid wiki recommendations: https://wiki.squid-cache.org/SquidFaq/ClearingTheCache
clean_cache() {
  local threshold="${CACHE_USAGE_THRESHOLD:-95}"
  if [ ! -d "${CACHE_DIR}" ]; then
    return
  fi
  local usage
  usage=$(df "${CACHE_DIR}" 2>/dev/null | awk 'NR==2 {print $5}' | sed 's/%//' || echo "0")
  if [ -n "${usage}" ] && [ "${usage}" -gt "${threshold}" ]; then
    echo "Cache usage: ${usage}% - cleaning cache on startup to prevent crash..."
    # Since we're on startup, Squid isn't running, so we skip "squid -k shutdown"
    # Remove aufs cache directories (16 first-level dirs: 00-0f in hex, case-insensitive)
    # Use [0-9a-fA-F] to match both lowercase (00-09) and uppercase (0A-0F) hex directories
    rm -rf "${CACHE_DIR}"/[0-9a-fA-F][0-9a-fA-F] 2>/dev/null || true
    # Remove swap state files
    rm -f "${CACHE_DIR}"/swap* 2>/dev/null || true
    # Remove network database files
    rm -f "${CACHE_DIR}"/netdb* 2>/dev/null || true
    # Remove log files
    rm -f "${CACHE_DIR}"/*.log 2>/dev/null || true
    echo "Cache cleaned. Will reinitialize with squid -z."
  fi
}

# Initialize the SSL certificate database if it doesn't exist.
# We check for the 'index.txt' file which is created by the certgen tool.
# We rely on the fsGroup in the Pod's securityContext to have the correct
# permissions on ${SPOOL_DIR}, so we can run this as the non-root squid user.
init_ssl_db() {
  if [ -f "${SSL_DB_DIR}/index.txt" ]; then
    echo "SSL certificate database already exists. Skipping initialization."
    return
  fi
  echo "SSL certificate database not found. Initializing..."
  /usr/lib64/squid/security_file_certgen -c -s "${SSL_DB_DIR}" -M 16MB
  echo "Initialization complete."
}

# Initialize cache directories
init_cache_dirs() {
  /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf -z
}

# Start squid (and optional per-site-exporter) in the background.
# Sets the global SQUID_PID variable to squid's PID so the cert watcher
# and final wait correctly track squid's lifecycle.
start_squid() {
  if [ "${1:-squid}" = "squid-with-per-site-exporter" ]; then
    shift || true
    # Start per-site-exporter first, reading from a FIFO, so we can
    # capture squid's PID directly rather than a wrapper shell's PID.
    local fifo="/tmp/access-log-fifo"
    mkfifo "$fifo"
    /usr/local/bin/per-site-exporter "$@" < "$fifo" &
    /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf > "$fifo" &
  else
    /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf "$@" &
  fi
  SQUID_PID=$!
}

# Return the SHA-256 hash of the TLS certificate file.
cert_hash() {
  sha256sum "$CERT_FILE" | awk '{print $1}'
}

# Monitor the TLS certificate for changes. When cert-manager renews the
# certificate, the mounted Secret volume is updated automatically. This loop
# detects the change and exits, causing the container to restart with the new
# certificate.
watch_cert() {
  local initial_hash
  initial_hash=$(cert_hash)
  while kill -0 "$SQUID_PID" 2>/dev/null; do
    current_hash=$(cert_hash)
    if [ -n "$current_hash" ] && [ "$current_hash" != "$initial_hash" ]; then
      echo "Certificate change detected, exiting to trigger container restart"
      exit 0
    fi
    # Run sleep in the background and wait for it so that SIGCHLD from a
    # squid crash interrupts the wait immediately (see CHLD trap above).
    sleep 30 &
    wait $!
  done
}

# Non-squid modes: exec directly and bypass all squid setup.
case "${1:-squid}" in
  exporter)
    shift || true
    exec /usr/local/bin/squid-exporter "$@"
    ;;
  icap-server)
    shift || true
    exec /usr/local/bin/icap-server "$@"
    ;;
esac

clean_cache
init_ssl_db
init_cache_dirs
start_squid "$@"
watch_cert

# If squid exited on its own, propagate its exit code.
wait "$SQUID_PID"

