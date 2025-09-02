#!/bin/bash

if [ "${1:-squid}" = "exporter" ]; then
  shift || true
  exec /usr/local/bin/squid-exporter "$@"
fi

SPOOL_DIR="/var/spool/squid"
SSL_DB_DIR="${SPOOL_DIR}/ssl_db"

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

/usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf -z

exec /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf "$@"
