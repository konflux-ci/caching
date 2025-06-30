# Squid Prometheus Exporter

A production-ready Squid Prometheus exporter solution providing comprehensive monitoring including liveness, bandwidth usage, hit/miss rates (general and per-site), and storage utilization.

## 🚀 Deployment Options

### Option 1: Integrated with Squid Helm Chart (Recommended)
Use the squid Helm chart with monitoring enabled for basic metrics:
```bash
# Basic monitoring with squid-exporter
helm install squid ../squid --set monitoring.enabled=true

# Or use the monitoring values file
helm install squid ../squid -f ../squid/values-monitoring.yaml
```

### Option 2: Standalone Advanced Deployment
Use this standalone deployment for advanced per-site metrics:
```bash
kubectl apply -f deployments/kubernetes.yaml
```

## 🤔 Which Option to Choose?

| Feature | Helm Chart Integration | Standalone Deployment |
|---------|----------------------|----------------------|
| **Basic Squid Metrics** | ✅ Included | ✅ Included |
| **Per-Site Analytics** | ❌ Not included | ✅ Full support |
| **Setup Complexity** | 🟢 Simple | 🟡 Moderate |
| **Resource Usage** | 🟢 Lower | 🟡 Higher |
| **Use Case** | General monitoring | Detailed analytics |

**Choose Helm Chart Integration if:**
- You need basic Squid monitoring
- You want simple deployment
- You're already using the squid Helm chart

**Choose Standalone Deployment if:**
- You need detailed per-site analytics
- You want advanced metrics and dashboards
- You need custom monitoring setup

## 🔄 Template Integration

The standalone deployment is **fully aligned** with the Helm chart templates:

### Shared Configuration Elements
- **Labels**: Uses same `app.kubernetes.io/*` label structure
- **Container Images**: Same image versions and configurations
- **Security Context**: Identical security settings
- **Resource Limits**: Aligned resource specifications
- **Probe Configuration**: Same health check patterns

### Key Alignments
```yaml
# Both use consistent labeling
labels:
  app.kubernetes.io/name: squid
  app.kubernetes.io/instance: squid-exporter
  app.kubernetes.io/component: proxy

# Same container configuration
containers:
- name: squid-exporter
  image: boynux/squid-exporter:v1.13.0
  resources:
    requests: { memory: "64Mi", cpu: "50m" }
    limits: { memory: "128Mi", cpu: "100m" }
```

This ensures **consistent behavior** whether deployed via Helm chart or standalone.

## 🎯 Architecture

This solution uses the **most mature** community exporter (`boynux/squid-exporter`) combined with a custom per-site metrics collector (`quay.io/konflux-ci/squid-per-site-metrics`):

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

### For Basic Monitoring (Most Users)
```bash
# Use the integrated Helm chart
helm install squid ../squid -f ../squid/values-monitoring.yaml

# Verify deployment
kubectl get pods -n proxy
kubectl port-forward -n proxy svc/squid 9301:9301
curl http://localhost:9301/metrics
```

### For Advanced Per-Site Analytics
```bash
# Deploy standalone advanced exporter (uses Quay image)
kubectl apply -f deployments/kubernetes.yaml

# Verify deployment
kubectl get pods -n squid-monitoring
kubectl port-forward -n squid-monitoring svc/squid-exporter 9301:9301 9302:9302

# Test both endpoints
curl http://localhost:9301/metrics  # Standard metrics
curl http://localhost:9302/metrics  # Per-site metrics
```

### Standalone Docker (Development)
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

### Per-Site Metrics (Port 9302) - Standalone Only
- `squid_site_requests_total{site="example.com", status="hit|miss"}` - Per-site hit/miss
- `squid_site_bytes_total{site="example.com"}` - Per-site bandwidth
- `squid_site_response_time_seconds{site="example.com", quantile="0.95"}` - Response times
- `squid_site_hit_ratio{site="example.com"}` - Hit ratio per site

## 🔧 Configuration

Both deployment methods use the same configuration approach, aligned for consistency.

### Environment Variables
- `SQUID_HOSTNAME=localhost` - Squid server hostname
- `SQUID_PORT=3128` - Squid server port
- `SQUID_EXPORTER_LISTEN=:9301` - Main exporter port
- `METRICS_PORT=9302` - Per-site metrics port (standalone only)
- `SQUID_LOG_PATH=/var/log/squid/access.log` - Log file path

### Container Images
- **Standard Exporter**: `boynux/squid-exporter:v1.13.0`
- **Per-Site Metrics**: `quay.io/konflux-ci/squid-per-site-metrics:v0.1`
- **Squid Proxy**: `localhost/konflux-ci/squid:latest`

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

## 🏗️ Template Structure Alignment

The standalone deployment mirrors the Helm chart structure:

### Kubernetes Resources
```yaml
# Namespace with consistent labels
apiVersion: v1
kind: Namespace
metadata:
  labels:
    app.kubernetes.io/name: squid
    app.kubernetes.io/instance: squid-exporter

# ServiceAccount (same as Helm chart)
apiVersion: v1
kind: ServiceAccount
metadata:
  name: squid-exporter
  labels:
    app.kubernetes.io/name: squid

# ConfigMap with aligned naming
apiVersion: v1
kind: ConfigMap
metadata:
  name: squid-exporter-config  # Matches Helm pattern: {{ fullname }}-config

# Service with same port structure
apiVersion: v1
kind: Service
spec:
  ports:
  - name: http      # Same port names as Helm chart
    port: 3128
    targetPort: http
  - name: metrics
    port: 9301
    targetPort: metrics
```

### Container Configuration
```yaml
# Main container (aligned with Helm chart)
- name: squid
  image: localhost/konflux-ci/squid:latest
  imagePullPolicy: IfNotPresent
  securityContext:
    runAsNonRoot: true
    runAsUser: 1001
    runAsGroup: 0

# Sidecar container (same as Helm chart)
- name: squid-exporter
  image: boynux/squid-exporter:v1.13.0
  resources:
    requests: { memory: "64Mi", cpu: "50m" }
    limits: { memory: "128Mi", cpu: "100m" }
```

This alignment ensures **predictable behavior** and **easy migration** between deployment methods.

## 📁 File Structure

```
exporter/
├── README.md                    # This file
├── deployments/
│   └── kubernetes.yaml          # Standalone deployment (aligned with Helm)
├── scripts/
│   └── per-site-metrics.py      # Per-site metrics collector
└── docs/
    ├── metrics-reference.md     # Complete metrics documentation
    └── troubleshooting.md       # Common issues and solutions
```

## 📈 Monitoring Setup

### Prometheus Configuration
```yaml
scrape_configs:
# For Helm chart deployment
- job_name: 'squid-helm'
  kubernetes_sd_configs:
  - role: service
  relabel_configs:
  - source_labels: [__meta_kubernetes_service_name]
    action: keep
    regex: squid  # Matches Helm chart service name

# For standalone deployment
- job_name: 'squid-standalone'
  static_configs:
  - targets: ['squid-exporter.squid-monitoring:9301', 'squid-exporter.squid-monitoring:9302']
```

## 🔍 Troubleshooting

### Deployment Verification
```bash
# For Helm chart deployment
kubectl get pods -n proxy
kubectl logs -n proxy deployment/squid -c squid-exporter

# For standalone deployment
kubectl get pods -n squid-monitoring
kubectl logs -n squid-monitoring deployment/squid-exporter -c squid-exporter
```

### Common Issues
1. **No metrics**: Check Squid cache manager ACLs
2. **Per-site metrics empty**: Verify log file access and Quay image availability
3. **Connection refused**: Check network policies and service endpoints
4. **Image pull errors**: Ensure `localhost/konflux-ci/squid:latest` is available

See `docs/troubleshooting.md` for detailed solutions.
