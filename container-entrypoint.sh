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
