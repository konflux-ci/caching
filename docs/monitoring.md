# Prometheus Monitoring

This chart includes comprehensive Prometheus monitoring capabilities through:

- Integrated upstream [squid-exporter](https://github.com/boynux/squid-exporter) for standard Squid metrics
- A per-site exporter that parses Squid access logs (via STDOUT piping) to emit per-host request metrics

Both exporters are bundled into the single Squid container image.

## Metrics Overview

The monitoring system provides detailed metrics about Squid's operational status:

- **Liveness**: Squid service information and connection status
- **Bandwidth Usage**: Client HTTP and Server HTTP traffic metrics
- **Hit/Miss Rates**: Cache performance metrics (general and detailed)
- **Storage Utilization**: Cache information and memory usage
- **Service Times**: Response times for different operations
- **Connection Information**: Active connections and request details

## Enabling Monitoring

Monitoring is enabled by default. To customize or disable it:

```yaml
# In values.yaml or via --set flags
squidExporter:
  enabled: true  # Set to false to disable
  port: 9301
  metricsPath: "/metrics"
  extractServiceTimes: "true"  # Enables detailed service time metrics
  resources:
    requests:
      cpu: 10m
      memory: 16Mi
    limits:
      cpu: 100m
      memory: 64Mi
```

Per-site exporter (enabled by default):

```yaml
# In values.yaml or via --set flags
perSiteExporter:
  enabled: true
  port: 9302
  metricsPath: "/metrics"
```

## Prometheus Integration

### Option 1: Prometheus Operator (Recommended)

If you're using Prometheus Operator, the chart automatically creates a ServiceMonitor:

```yaml
prometheus:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    namespace: ""  # Leave empty to use the same namespace as the app
```

When enabled, the ServiceMonitor exposes endpoints for both exporters: `9301` (standard) and `9302` (per-site).

### Option 2: Manual Prometheus Configuration

**ServiceMonitor CRD Included:**

The chart includes the ServiceMonitor CRD in the `crds/` directory, so it will be automatically installed by Helm when the chart is deployed.

If you don't want ServiceMonitor functionality, you can disable it:
```bash
helm install squid ./squid --set prometheus.serviceMonitor.enabled=false
```

**For non-Prometheus Operator setups**, disable ServiceMonitor and use manual Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'squid-proxy'
    static_configs:
      - targets: ['squid.caching.svc.cluster.local:9301']
    scrape_interval: 30s
    metrics_path: '/metrics'
```

**Complete example**: See `docs/prometheus-config-example.yaml` in this repository for a full Prometheus configuration file.

## Available Metrics

### Standard Squid Metrics (Port 9301)

- `squid_client_http_requests_total`: Total client HTTP requests
- `squid_client_http_hits_total`: Total client HTTP hits
- `squid_client_http_errors_total`: Total client HTTP errors
- `squid_server_http_requests_total`: Total server HTTP requests
- `squid_cache_memory_bytes`: Cache memory usage
- `squid_cache_disk_bytes`: Cache disk usage
- `squid_service_times_seconds`: Service times for different operations
- `squid_up`: Squid availability status

### Per-site Metrics (Port 9302)

- `squid_site_requests_total{host="<hostname>"}`: Total requests per origin host
- `squid_site_hits_total{host="<hostname>"}`: Cache hits per host
- `squid_site_misses_total{host="<hostname>"}`: Cache misses per host
- `squid_site_bytes_total{host="<hostname>"}`: Bytes transferred per host
- `squid_site_hit_ratio{host="<hostname>"}`: Hit ratio gauge per host
- `squid_site_response_time_seconds{host="<hostname>",le="..."}`: Response time histogram per host

## Accessing Metrics

### Via Port Forward

```bash
# Forward the standard squid-exporter metrics port
kubectl port-forward -n caching svc/squid 9301:9301

# View standard metrics in your browser or with curl
curl http://localhost:9301/metrics
```

Per-site exporter metrics:

```bash
# Forward the per-site exporter metrics port
kubectl port-forward -n caching svc/squid 9302:9302

# View per-site metrics
curl http://localhost:9302/metrics
```

### Via Service

The metrics are exposed on the service:

```bash
# Standard squid-exporter metrics (from within the cluster)
curl http://squid.caching.svc.cluster.local:9301/metrics

# Per-site exporter metrics (from within the cluster)
curl http://squid.caching.svc.cluster.local:9302/metrics
```

## Troubleshooting Metrics

See the [troubleshooting guide](troubleshooting.md#metrics-troubleshooting) for common monitoring issues.
