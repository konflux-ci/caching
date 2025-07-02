FROM python:3.11-slim

# Metadata
LABEL maintainer="squid-exporter-team"
LABEL description="Squid per-site metrics collector for Prometheus"
LABEL version="1.0.0"

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Create application directory
WORKDIR /app

# Copy the per-site metrics script
COPY scripts/per-site-metrics.py /app/per-site-metrics.py

# Make script executable
RUN chmod +x /app/per-site-metrics.py

# Create non-root user for security
RUN groupadd -r metrics && useradd -r -g metrics -u 1001 metrics-user
RUN chown -R metrics-user:metrics /app
USER metrics-user

# Health check endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD curl -f http://localhost:${METRICS_PORT:-9302}/health || exit 1

# Expose metrics port
EXPOSE 9302

# Run the metrics collector
CMD ["python", "/app/per-site-metrics.py"] 
