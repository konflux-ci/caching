# Squid Prometheus Exporter

A production-ready Squid Prometheus exporter solution providing comprehensive monitoring including liveness, bandwidth usage, hit/miss rates (general and per-site), and storage utilization.

## 🎯 Architecture

This solution uses the **most mature** community exporter (`boynux/squid-exporter`) combined with a custom per-site metrics collector:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Squid Proxy   │────│  Squid Exporter  │────│   Prometheus    │
│   :3128         │    │  :9301           │    │                 │
└─────────────────┘    └──────────────────┘    └─────────────────┘
          │                                              │
          ▼                                              ▼
┌─────────────────┐                             ┌─────────────────┐
│ Per-Site Logger │                             │     Grafana     │
│   :9302         │─────────────────────────────│   Dashboard     │
└─────────────────┘                             └─────────────────┘
```

## ✅ Requirements Coverage

| Requirement | Implementation | Metrics | Status |
|-------------|----------------|---------|---------|
| **Liveness** | boynux/squid-exporter | `up`, `squid_up` | ✅ Complete |
| **Bandwidth** | boynux/squid-exporter | `squid_server_http_kbytes_out_kbytes_total` | ✅ Complete |
| **Hit/Miss Rate** | boynux/squid-exporter | `squid_client_http_hits_total` / `squid_client_http_requests_total` | ✅ Complete |
| **Per-site Hit/Miss** | Custom log parser | `squid_site_requests_total{site="...", status="hit\|miss"}` | ✅ Complete |
| **Storage** | boynux/squid-exporter | `squid_swap_ins_total`, `squid_swap_outs_total` | ✅ Complete |

## 🚀 Quick Start

### Option 1: Custom Container (Recommended)
```bash
# Build and deploy
make build-image
make deploy

# Verify
make verify
```

### Option 2: Standalone Docker
```bash
docker run -d -p 9301:9301 \
  -e SQUID_HOSTNAME=your-squid-host \
  boynux/squid-exporter:v1.13.0
```

## 📊 Key Metrics

### Core Metrics (Port 9301)
- `up` - Exporter liveness
- `squid_client_http_requests_total` - Total requests
- `squid_client_http_hits_total` - Cache hits
- `squid_server_http_kbytes_out_kbytes_total` - Outgoing bandwidth
- `squid_swap_ins_total` / `squid_swap_outs_total` - Storage utilization

### Per-Site Metrics (Port 9302)
- `squid_site_requests_total{site="example.com", status="hit|miss"}` - Per-site hit/miss
- `squid_site_bytes_total{site="example.com"}` - Per-site bandwidth
- `squid_site_response_time_seconds{site="example.com", quantile="0.95"}` - Response times
- `squid_site_hit_ratio{site="example.com"}` - Hit ratio per site

## 🔧 Configuration

### Environment Variables
- `SQUID_HOSTNAME=localhost` - Squid server hostname
- `SQUID_PORT=3128` - Squid server port
- `SQUID_EXPORTER_LISTEN=:9301` - Main exporter port
- `METRICS_PORT=9302` - Per-site metrics port

### Squid Configuration
```squid
# Allow Prometheus access to cache manager
acl prometheus src 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16
http_access allow localhost manager
http_access allow prometheus manager
http_access deny manager

# Enable logging for per-site metrics
access_log /var/log/squid/access.log squid
```

## 📁 File Structure

```
exporter/
├── README.md                    # This file
├── Makefile                     # Build automation
├── Dockerfile                   # Custom container image
├── deployments/
│   ├── kubernetes.yaml          # Complete K8s deployment
│   └── squid-config.yaml        # Squid ConfigMap
├── scripts/
│   └── per-site-metrics.py      # Per-site metrics collector
└── docs/
    ├── metrics-reference.md     # Complete metrics documentation
    └── troubleshooting.md       # Common issues and solutions
```

## 🛠️ Development

```bash
# Build everything
make build

# Deploy to Kubernetes
make deploy

# Check status
make status

# View logs
make logs

# Clean up
make clean
```

## 📈 Monitoring Setup

### Prometheus Configuration
```yaml
scrape_configs:
- job_name: 'squid-exporter'
  static_configs:
  - targets: ['squid-exporter:9301', 'squid-exporter:9302']
```

### Grafana Dashboard
Import the provided dashboard from `docs/grafana-dashboard.json`

## 🔍 Troubleshooting

### Common Issues
1. **No metrics**: Check Squid cache manager ACLs
2. **Per-site metrics empty**: Verify log file access
3. **Connection refused**: Check network policies

See `docs/troubleshooting.md` for detailed solutions.
