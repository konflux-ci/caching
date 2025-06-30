# Squid Prometheus Exporter Deployment

## Quick Start

### 1. Build the Container Image
```bash
# Using Podman (recommended for Fedora/RHEL)
podman build -t squid-per-site-metrics .

# Or using Docker
docker build -t squid-per-site-metrics .
```

### 2. Test Locally
```bash
# Create sample log file
mkdir -p test-logs
cat > test-logs/access.log << 'EOF'
1720000000.123 100 192.168.1.100 TCP_HIT/200 1024 GET http://example.com/page1 - DIRECT/example.com text/html
1720000001.456 200 192.168.1.101 TCP_MISS/200 2048 GET http://google.com/search - DIRECT/google.com text/html
EOF

# Run container locally (choose one option)

# Run container locally (use the Z flag for SELinux systems like Fedora/RHEL)
podman run -it --rm \
  -p 9302:9302 \
  -v "$(pwd)/test-logs:/var/log/squid:ro,Z" \
  -e SQUID_LOG_PATH=/var/log/squid/access.log \
  --user root \
  squid-per-site-metrics

# Alternative: Use different port if 9302 is busy
podman run -it --rm \
  -p 9303:9302 \
  -v "$(pwd)/test-logs:/var/log/squid:ro,Z" \
  -e SQUID_LOG_PATH=/var/log/squid/access.log \
  --user root \
  squid-per-site-metrics
```

### 3. Deploy to Kubernetes
```bash
# Apply the main deployment
kubectl apply -f deployments/kubernetes.yaml

# Optional: If you have Prometheus Operator installed
kubectl apply -f deployments/servicemonitor.yaml

# Check status
kubectl get pods -l app=squid-exporter
```

## Architecture

The deployment includes:
- **Main Exporter**: `boynux/squid-exporter` (port 9301) - Standard Squid metrics
- **Per-Site Metrics**: Custom Python script (port 9302) - Site-specific traffic analysis

## Accessing Metrics

### Port Forward for Testing
```bash
kubectl port-forward service/squid-exporter 9301:9301 9302:9302
```

### Check Endpoints
```bash
# Standard Squid metrics
curl http://localhost:9301/metrics

# Per-site metrics  
curl http://localhost:9302/health
```

## Configuration

### Environment Variables
- `SQUID_LOG_PATH`: Path to Squid access log (default: `/var/log/squid/access.log`)
- `METRICS_PORT`: Port for per-site metrics server (default: `9302`)

### Log Format
Expects standard Squid access log format:
```
timestamp elapsed remotehost code/status bytes method URL rfc931 peerstatus/peerhost type
```

## Troubleshooting

### Container Won't Start
```bash
# Check container logs
podman logs <container-name>

# Verify log file exists and is readable
ls -la /var/log/squid/access.log
```

### Permission Denied Errors
If you see `Permission denied: '/var/log/squid/access.log'`:

```bash
# SELinux issue (common on Fedora/RHEL) - add Z flag
podman run -v "$(pwd)/test-logs:/var/log/squid:ro,Z" ...

# Port conflict - use different host port
podman run -p 9303:9302 ...

# Check what's using the port
ss -tlnp | grep :9302

# Stop containers using the port
podman stop $(podman ps -q --filter "publish=9302") 2>/dev/null || true
```

### No Metrics Data
```bash
# Check if Squid is writing logs
tail -f /var/log/squid/access.log

# Verify Python script can read logs
python3 scripts/per-site-metrics.py /var/log/squid/access.log
```

### Kubernetes Issues
```bash
# Check pod status
kubectl describe pod -l app=squid-exporter

# Check logs
kubectl logs -l app=squid-exporter --all-containers=true
```

### ServiceMonitor Errors
If you see `no matches for kind "ServiceMonitor"`:

```bash
# ServiceMonitor requires Prometheus Operator
# Either install Prometheus Operator or skip ServiceMonitor:
kubectl apply -f deployments/kubernetes.yaml  # Main deployment only

# To install Prometheus Operator (optional):
kubectl apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/bundle.yaml
```

## Clean Up
```bash
# Remove Kubernetes deployment
kubectl delete -f deployments/kubernetes.yaml

# Remove local images
podman rmi squid-per-site-metrics
``` 