#!/bin/bash

if [ "${1:-squid}" = "exporter" ]; then
  shift || true
  exec /usr/local/bin/squid-exporter "$@"
fi

if [ "${1:-squid}" = "icap-server" ]; then
  shift || true
  exec /usr/local/bin/icap-server "$@"
fi

SPOOL_DIR="/var/spool/squid"
SSL_DB_DIR="${SPOOL_DIR}/ssl/db"
CACHE_DIR="${SPOOL_DIR}/cache"

# Check cache disk usage on startup and clean if almost full
# This handles cases where Squid crashed with a full cache
CACHE_USAGE_THRESHOLD="${CACHE_USAGE_THRESHOLD:-95}"
if [ -d "${CACHE_DIR}" ]; then
  USAGE=$(df "${CACHE_DIR}" 2>/dev/null | awk 'NR==2 {print $5}' | sed 's/%//' || echo "0")
  if [ -n "${USAGE}" ] && [ "${USAGE}" -gt "${CACHE_USAGE_THRESHOLD}" ]; then
    echo "Cache usage: ${USAGE}% - cleaning cache on startup to prevent crash..."
    # Clear cache following Squid wiki recommendations: https://wiki.squid-cache.org/SquidFaq/ClearingTheCache
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
fi

# Check if the ssl_db directory exists and has been initialized.
# We check for the 'index.txt' file which is created by the certgen tool.
if [ ! -f "${SSL_DB_DIR}/index.txt" ]; then
  echo "SSL certificate database not found. Initializing..."

  # We rely on the fsGroup in the Pod's securityContext to have the correct
  # permissions on ${SPOOL_DIR}, so we can run this as the non-root squid user.
  /usr/lib64/squid/security_file_certgen -c -s "${SSL_DB_DIR}" -M 16MB

  echo "Initialization complete."
else
  echo "SSL certificate database already exists. Skipping initialization."
fi

# Initialize cache directories
/usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf -z

# Sidecar: per-site-exporter - stream access.log into the exporter
if [ "${1:-squid}" = "squid-with-per-site-exporter" ]; then
  shift || true
  exec bash -lc '/usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf | /usr/local/bin/per-site-exporter "$@"' _ "$@"
fi

exec /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf "$@"
