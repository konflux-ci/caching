# Troubleshooting Guide

This guide helps you diagnose and fix common issues with the Squid Prometheus exporter.

## 🚨 Common Issues

### 1. No Metrics Available

**Symptoms:**
- Prometheus shows no targets or targets are down
- `/metrics` endpoints return connection refused

**Diagnosis:**
```bash
# Check if pods are running
kubectl get pods -l app=squid-exporter

# Check pod logs
kubectl logs -l app=squid-exporter --all-containers=true

# Test connectivity
kubectl port-forward service/squid-exporter 9301:9301 9302:9302
curl http://localhost:9301/metrics
curl http://localhost:9302/health
```

**Solutions:**
1. **Pod not starting:** Check resource limits and image availability
2. **Network issues:** Verify service configuration and firewall rules
3. **Configuration errors:** Review ConfigMap and environment variables

### 2. Squid Exporter Shows "Connection Refused"

**Symptoms:**
- Main exporter (port 9301) returns connection errors
- `squid_up` metric shows 0

**Diagnosis:**
```bash
# Check if Squid is running
kubectl exec -it deployment/squid-exporter -c squid -- squid -v

# Test Squid cache manager
kubectl exec -it deployment/squid-exporter -c squid -- curl http://localhost:3128/squid-internal-mgr/info
```

**Solutions:**
1. **Squid not started:** Check Squid container logs
   ```bash
   kubectl logs deployment/squid-exporter -c squid
   ```

2. **Cache manager access denied:** Update `squid.conf`:
   ```squid
   acl prometheus src 127.0.0.1
   http_access allow prometheus manager
   ```

3. **Wrong hostname/port:** Check exporter environment variables:
   ```yaml
   env:
   - name: SQUID_HOSTNAME
     value: "localhost"  # Should match Squid container
   - name: SQUID_PORT
     value: "3128"
   ```

### 3. Per-Site Metrics Empty

**Symptoms:**
- `/metrics` endpoint on port 9302 returns empty or no site-specific metrics
- Health check shows 0 sites monitored

**Diagnosis:**
```bash
# Check if log file exists and has content
kubectl exec -it deployment/squid-exporter -c per-site-metrics -- ls -la /var/log/squid/
kubectl exec -it deployment/squid-exporter -c per-site-metrics -- tail -f /var/log/squid/access.log

# Check per-site metrics health
curl http://localhost:9302/health
```

**Solutions:**
1. **Log file not found:**
   - Verify volume mount in deployment
   - Check if Squid is writing logs:
     ```yaml
     volumeMounts:
     - name: squid-logs
       mountPath: /var/log/squid
     ```

2. **Log format mismatch:** Update regex in `per-site-metrics.py`
3. **No traffic:** Generate test traffic through Squid proxy

### 4. High Memory Usage

**Symptoms:**
- Pods getting OOMKilled
- High memory consumption in monitoring

**Diagnosis:**
```bash
# Check resource usage
kubectl top pods -l app=squid-exporter

# Check memory limits
kubectl describe pod -l app=squid-exporter
```

**Solutions:**
1. **Increase memory limits:**
   ```yaml
   resources:
     limits:
       memory: "2Gi"  # Increase as needed
   ```

2. **Optimize per-site metrics:** Reduce response time history:
   ```python
   # In per-site-metrics.py, reduce from 1000 to 100
   if len(self.response_times[site]) >= 100:
   ```

### 5. Metrics Inconsistency

**Symptoms:**
- Different hit rates between main exporter and per-site metrics
- Numbers don't add up

**Diagnosis:**
```bash
# Compare metrics
curl http://localhost:9301/metrics | grep -E "(hits|requests)_total"
curl http://localhost:9302/metrics | grep -E "site_requests_total"
```

**Solutions:**
1. **Time sync issues:** Ensure all containers use same timezone
2. **Log parsing errors:** Check per-site metrics logs for parse failures
3. **Different time windows:** Use same time ranges in queries

## 🔧 Debugging Commands

### Check All Components
```bash
# Quick health check
make verify

# Detailed status
make status

# View all logs
make logs
```

### Manual Testing
```bash
# Test Squid directly
kubectl port-forward service/squid-exporter 3128:3128
curl -x http://localhost:3128 http://example.com

# Test metrics endpoints
kubectl port-forward service/squid-exporter 9301:9301 9302:9302
curl http://localhost:9301/metrics | head -20
curl http://localhost:9302/metrics | head -20
curl http://localhost:9302/health
```

### Container Debugging
```bash
# Get shell in containers
kubectl exec -it deployment/squid-exporter -c squid -- /bin/bash
kubectl exec -it deployment/squid-exporter -c squid-exporter -- /bin/sh
kubectl exec -it deployment/squid-exporter -c per-site-metrics -- /bin/bash

# Check processes
kubectl exec -it deployment/squid-exporter -c squid -- ps aux
kubectl exec -it deployment/squid-exporter -c per-site-metrics -- ps aux
```

## 📊 Performance Tuning

### Squid Configuration
```squid
# Increase cache memory
cache_mem 512 MB

# Optimize disk cache
cache_dir ufs /var/cache/squid 2000 16 256

# Reduce log verbosity if needed
debug_options ALL,1
```

### Resource Optimization
```yaml
# Adjust based on your traffic
resources:
  requests:
    memory: "256Mi"
    cpu: "100m"
  limits:
    memory: "1Gi"
    cpu: "500m"
```

## 🚦 Health Checks

### Automated Health Checks
```bash
#!/bin/bash
# health-check.sh

echo "🔍 Checking Squid Exporter Health..."

# Check if pods are ready
kubectl wait --for=condition=ready pod -l app=squid-exporter --timeout=60s

# Check main exporter
if curl -sf http://localhost:9301/metrics > /dev/null; then
    echo "✅ Main exporter: OK"
else
    echo "❌ Main exporter: FAILED"
fi

# Check per-site metrics
if curl -sf http://localhost:9302/health > /dev/null; then
    echo "✅ Per-site metrics: OK"
else
    echo "❌ Per-site metrics: FAILED"
fi

# Check Squid proxy
if curl -sf --proxy http://localhost:3128 http://example.com > /dev/null; then
    echo "✅ Squid proxy: OK"
else
    echo "❌ Squid proxy: FAILED"
fi
```

### Prometheus Alerts
```yaml
# Add to your Prometheus rules
groups:
- name: squid-exporter
  rules:
  - alert: SquidExporterDown
    expr: up{job="squid-exporter"} == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "Squid exporter is down"

  - alert: SquidProxyDown
    expr: squid_up == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "Squid proxy is not responding"

  - alert: NoPerSiteMetrics
    expr: absent(squid_site_requests_total)
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Per-site metrics not available"
```

## 🆘 Getting Help

### Collect Debug Information
```bash
# Create debug bundle
kubectl get pods,svc,configmap -l app=squid-exporter -o yaml > debug-info.yaml
kubectl logs -l app=squid-exporter --all-containers=true > debug-logs.txt
kubectl describe pods -l app=squid-exporter > debug-describe.txt

# Test connectivity
kubectl port-forward service/squid-exporter 9301:9301 9302:9302 3128:3128 &
curl -v http://localhost:9301/metrics > main-metrics.txt 2>&1
curl -v http://localhost:9302/health > per-site-health.txt 2>&1
curl -v --proxy http://localhost:3128 http://httpbin.org/ip > proxy-test.txt 2>&1
```

### Log Analysis
```bash
# Check for common error patterns
kubectl logs -l app=squid-exporter --all-containers=true | grep -i error
kubectl logs -l app=squid-exporter --all-containers=true | grep -i "connection refused"
kubectl logs -l app=squid-exporter --all-containers=true | grep -i "permission denied"
```

### Support Channels
- 📖 Check the metrics reference: `docs/metrics-reference.md`
- 🐛 Create GitHub issue with debug information
- 💬 Community forums for general questions 