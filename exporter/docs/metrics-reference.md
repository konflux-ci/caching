# Squid Exporter Metrics Reference

This document provides a complete reference for all metrics exposed by the Squid Prometheus exporter solution.

## 📊 Core Metrics (Port 9301)

These metrics are provided by the `boynux/squid-exporter` and cover general Squid performance.

### Liveness & Health

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `up` | gauge | Exporter liveness (1 = up, 0 = down) | - |
| `squid_up` | gauge | Squid proxy availability | - |

### Request Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_client_http_requests_total` | counter | Total HTTP requests from clients | - |
| `squid_client_http_hits_total` | counter | Total cache hits | - |
| `squid_client_http_errors_total` | counter | Total HTTP errors | - |
| `squid_client_http_kbytes_in_kbytes_total` | counter | Total kilobytes received from clients | - |
| `squid_client_http_kbytes_out_kbytes_total` | counter | Total kilobytes sent to clients | - |

### Server Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_server_http_requests_total` | counter | Total requests to origin servers | - |
| `squid_server_http_errors_total` | counter | Total server errors | - |
| `squid_server_http_kbytes_in_kbytes_total` | counter | Total kilobytes received from servers | - |
| `squid_server_http_kbytes_out_kbytes_total` | counter | **Outgoing bandwidth** - Total kilobytes sent to servers | - |

### Storage & Cache Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_swap_outs_total` | counter | **Storage utilization** - Objects written to disk | - |
| `squid_swap_ins_total` | counter | **Storage utilization** - Objects read from disk | - |
| `squid_swap_files_cleaned_total` | counter | Cache files cleaned | - |

### Performance Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_client_http_hit_kbytes_out_bytes_total` | counter | Cache hit bandwidth | - |
| `squid_client_http_hit_service_time_seconds` | histogram | Service time for cache hits | - |
| `squid_client_http_miss_service_time_seconds` | histogram | Service time for cache misses | - |

## 🎯 Per-Site Metrics (Port 9302)

These metrics are provided by our custom log parser and offer site-specific insights.

### Site Request Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_site_requests_total` | counter | **Per-site hit/miss rates** - Total requests per site | `site`, `status` (hit/miss) |

**Example:**
```
squid_site_requests_total{site="example.com",status="hit"} 150
squid_site_requests_total{site="example.com",status="miss"} 50
squid_site_requests_total{site="google.com",status="hit"} 200
squid_site_requests_total{site="google.com",status="miss"} 25
```

### Site Performance Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|---------|
| `squid_site_hit_ratio` | gauge | **Hit ratio per site** (0.0 to 1.0) | `site` |

**Example:**
```
squid_site_hit_ratio{site="example.com"} 0.750
squid_site_hit_ratio{site="google.com"} 0.889
```

## 📈 Calculated Metrics

You can create these derived metrics in Prometheus or Grafana:

### Overall Hit Rate
```promql
sum(squid_client_http_hits_total) / sum(squid_client_http_requests_total)
```

### Per-Site Hit Rate
```promql
squid_site_requests_total{status="hit"} / 
(squid_site_requests_total{status="hit"} + squid_site_requests_total{status="miss"})
```

### Top Sites by Requests
```promql
topk(10, sum by (site) (squid_site_requests_total))
```

### Cache Miss Rate
```promql
1 - (sum(squid_client_http_hits_total) / sum(squid_client_http_requests_total))
```

### Bandwidth Utilization Rate
```promql
rate(squid_server_http_kbytes_out_kbytes_total[5m]) * 8 * 1024  # Convert to bits/sec
```

## 🎛️ Grafana Dashboard Queries

### Panel: Hit Rate Over Time
```promql
rate(squid_client_http_hits_total[5m]) / rate(squid_client_http_requests_total[5m])
```

### Panel: Top 10 Sites by Requests
```promql
topk(10, sum by (site) (rate(squid_site_requests_total[5m])))
```

### Panel: Bandwidth Usage
```promql
rate(squid_server_http_kbytes_out_kbytes_total[5m]) * 1024  # KB/s
```

### Panel: Cache Storage Growth
```promql
rate(squid_swap_outs_total[5m]) - rate(squid_swap_ins_total[5m])
```

## 🚨 Alerting Rules

### High Miss Rate Alert
```yaml
- alert: HighCacheMissRate
  expr: (1 - (sum(rate(squid_client_http_hits_total[5m])) / sum(rate(squid_client_http_requests_total[5m])))) > 0.5
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Squid cache miss rate is high"
    description: "Cache miss rate is {{ $value | humanizePercentage }} for 5 minutes"
```

### Squid Down Alert
```yaml
- alert: SquidDown
  expr: up{job="squid-exporter"} == 0
  for: 1m
  labels:
    severity: critical
  annotations:
    summary: "Squid exporter is down"
    description: "Squid exporter has been down for more than 1 minute"
```

### High Bandwidth Usage Alert
```yaml
- alert: HighBandwidthUsage
  expr: rate(squid_server_http_kbytes_out_kbytes_total[5m]) * 1024 > 100000000  # 100MB/s
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "High bandwidth usage detected"
    description: "Outgoing bandwidth is {{ $value | humanize }}B/s"
```

## 🔧 Troubleshooting Metrics

### No Metrics Available
1. Check if exporters are running: `up` metric should be 1
2. Verify Squid is accessible: `squid_up` should be 1
3. Check network connectivity between exporter and Squid

### Per-Site Metrics Empty
1. Verify log file access: Check `/var/log/squid/access.log`
2. Ensure log format is correct
3. Check per-site exporter health: `curl http://localhost:9302/health`

### Inconsistent Hit Rates
1. Compare `squid_client_http_hits_total` vs per-site aggregates
2. Check log parsing regex in per-site collector
3. Verify time synchronization between components

## 📚 References

- [Squid Cache Manager](http://www.squid-cache.org/Doc/config/cache_mgr/)
- [boynux/squid-exporter Documentation](https://github.com/boynux/squid-exporter)
- [Prometheus Metric Types](https://prometheus.io/docs/concepts/metric_types/) 