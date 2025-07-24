#!/bin/bash

set -euo pipefail

# Configuration from environment variables
ENABLE_PER_SITE_EXPORTER="${ENABLE_PER_SITE_EXPORTER:-false}"
# Note: WEB_LISTEN_ADDRESS, WEB_TLS_CERT_FILE, WEB_TLS_KEY_FILE are read directly by the Go application

start_per_site_exporter() {
    # Configuration is handled via environment variables (see main.go)
    # The Go application reads WEB_LISTEN_ADDRESS, WEB_TLS_CERT_FILE, WEB_TLS_KEY_FILE
    local listen_addr="${WEB_LISTEN_ADDRESS:-:9302}"
    
    if [[ -n "${WEB_TLS_CERT_FILE:-}" && -n "${WEB_TLS_KEY_FILE:-}" ]]; then
        echo "Starting per-site exporter with TLS on $listen_addr"
    else
        echo "Starting per-site exporter on $listen_addr"
    fi
    
    # No need to pass flags - the Go app reads environment variables directly
    exec /usr/local/bin/squid-per-site-exporter
}

start_squid() {
    echo "Initializing squid cache directory..."
    /usr/sbin/squid -f /etc/squid/squid.conf -z
    
    # Remove PID file created by -z initialization to avoid conflicts
    rm -f /run/squid/squid.pid

    echo "Starting squid proxy..."
    # Note: access_log goes to stdout (configured in squid.conf), cache_log goes to stderr
    # Remove -d flag to avoid mixing debug output with access logs
    # Pass all script arguments to squid
    exec /usr/sbin/squid --foreground -f /etc/squid/squid.conf "$@"
}

# Main execution logic
if [[ "$ENABLE_PER_SITE_EXPORTER" == "true" ]]; then
    echo "Starting squid with per-site metrics exporter..."
    
    # Create a named pipe for log streaming
    LOG_PIPE="/tmp/squid-access-logs"
    echo "Creating named pipe: $LOG_PIPE"
    mkfifo "$LOG_PIPE" || true
    
    # Start the per-site exporter in background, reading from the named pipe
    echo "Starting per-site exporter in background..."
    (cat "$LOG_PIPE" | start_per_site_exporter) &
    EXPORTER_PID=$!
    
    # Give the exporter a moment to start
    sleep 2
    
    # Override access_log destination via environment variable approach
    # Create a temporary squid.conf that uses our named pipe
    TEMP_CONF="/tmp/squid.conf"
    cp /etc/squid/squid.conf "$TEMP_CONF"
    sed "s|access_log stdio:/dev/stdout squid|access_log stdio:$LOG_PIPE squid|g" "$TEMP_CONF" > /tmp/squid-modified.conf
    
    # Start squid with the modified config as the main process
    echo "Starting squid as main process with log streaming..."
    exec /usr/sbin/squid --foreground -f /tmp/squid-modified.conf "$@"
else
    echo "Starting squid without per-site exporter..."
    # Pass all script arguments to start_squid
    start_squid "$@"
fi
