#!/bin/bash

if [ "${1:-squid}" = "exporter" ]; then
  shift || true
  exec /usr/local/bin/squid-exporter "$@"
fi

/usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf -z

exec /usr/sbin/squid -d 1 --foreground -f /etc/squid/squid.conf "$@"
