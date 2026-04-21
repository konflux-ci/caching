# Monitoring Integration Tests

This document provides step-by-step testing instructions for validating the Squid proxy monitoring integration.

## Complete Monitoring Integration Tests

### 1. Pre-Test Setup

#### 1.1 Verify Cluster Connectivity
```bash
# Test basic cluster access
kubectl cluster-info

# Show current context
kubectl config current-context

# Verify nodes are ready
kubectl get nodes
```

**Expected Result**: Cluster information displays correctly with no errors.

#### 1.2 Install Prometheus Operator (if needed)

> **Note**: We pin to a specific release (v0.90.1) rather than `main` to ensure reproducibility 
> and avoid applying unreviewed changes from upstream.

```bash
# Check if ServiceMonitor CRD exists
kubectl get crd servicemonitors.monitoring.coreos.com

# If the CRD doesn't exist, install Prometheus Operator (pinned to v0.90.1)
if ! kubectl get crd servicemonitors.monitoring.coreos.com &> /dev/null; then
  echo "Installing Prometheus Operator..."
  kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.90.1/bundle.yaml

  # Wait for CRDs to be established
  kubectl wait --for condition=established --timeout=60s crd/servicemonitors.monitoring.coreos.com
fi
```

**Expected Result**: ServiceMonitor CRD is available for monitoring integration.

#### 1.3 Clean Previous Deployments
```bash
# Remove any existing deployment
helm uninstall squid 2>/dev/null || true

# Remove namespace
kubectl delete namespace caching 2>/dev/null || true

# Wait for cleanup
sleep 10
```

**Expected Result**: Clean environment with no conflicts.

### 2. Deployment Tests

#### 2.1 Deploy Squid with Monitoring
```bash
# Deploy with monitoring enabled
helm install squid ./squid \
  --set squidExporter.enabled=true \
  --set prometheus.serviceMonitor.enabled=true \
  --set cert-manager.enabled=false \
  --wait --timeout=300s
```

**Expected Result**: Deployment succeeds with `STATUS: deployed`.

#### 2.2 Verify Pod Readiness
```bash
# Wait for pods to be ready
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=squid -n caching --timeout=120s

# Check pod status
kubectl get pods -n caching -o wide
```

**Expected Result**: Pod shows `2/2 Running` (squid + squid-exporter containers).

#### 2.3 Verify Service Creation
```bash
# Check service
kubectl get svc -n caching

# Check service details
kubectl describe svc squid -n caching
```

**Expected Result**: Service exposes ports 3128 (proxy) and 9301 (metrics).

#### 2.4 Verify ServiceMonitor (if Prometheus Operator available)
```bash
# Check ServiceMonitor
kubectl get servicemonitor -n caching

# Check ServiceMonitor details
kubectl describe servicemonitor squid -n caching
```

**Expected Result**: ServiceMonitor created with correct selector and endpoints.

### 3. Container Health Tests

#### 3.1 Verify Container Configuration
```bash
# Get pod name
POD_NAME=$(kubectl get pods -n caching -l app.kubernetes.io/name=squid -o jsonpath="{.items[0].metadata.name}")
echo "Testing pod: $POD_NAME"

# Check container names
kubectl get pod $POD_NAME -n caching -o jsonpath='{.spec.containers[*].name}'
```

**Expected Result**: Shows both `squid` and `squid-exporter` containers.

#### 3.2 Check Container Logs
```bash
# Check squid container logs
kubectl logs -n caching $POD_NAME -c squid --tail=10

# Check squid-exporter container logs
kubectl logs -n caching $POD_NAME -c squid-exporter --tail=10
```

**Expected Result**:
- Squid logs show successful startup with no permission errors
- Squid-exporter logs show successful connection to cache manager

### 4. Metrics Endpoint Tests

#### 4.1 Test Direct Metrics Access
```bash
# Port forward to metrics endpoint
kubectl port-forward -n caching $POD_NAME 9301:9301 &
PF_PID=$!
sleep 3

# Test metrics endpoint
curl -s http://localhost:9301/metrics | head -20

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Prometheus metrics displayed starting with `# HELP` and `# TYPE` comments.

#### 4.2 Test Service-Based Metrics Access
```bash
# Port forward via service
kubectl port-forward -n caching svc/squid 9301:9301 &
PF_PID=$!
sleep 3

# Test metrics via service
curl -s http://localhost:9301/metrics | grep -c "^squid_"

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Number of squid metrics found (typically 20+ metrics).

#### 4.3 Verify Specific Metrics
```bash
# Port forward for detailed metrics check
kubectl port-forward -n caching svc/squid 9301:9301 &
PF_PID=$!
sleep 3

# Check for key metrics
curl -s http://localhost:9301/metrics | grep -E "squid_up|squid_client_http|squid_cache_"

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Shows key metrics like:
- `squid_up 1` (service health)
- `squid_client_http_requests_total` (request counters)
- `squid_cache_memory_bytes` (cache stats)

### 5. Cache Manager Tests

#### 5.1 Test Cache Manager Access
```bash
# Port forward to proxy port
kubectl port-forward -n caching $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test cache manager info
curl -s http://localhost:3128/squid-internal-mgr/info | head -10

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Cache manager information displayed (version, uptime, etc.).

#### 5.2 Test Cache Manager Counters
```bash
# Port forward to proxy port
kubectl port-forward -n caching $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test cache manager counters
curl -s http://localhost:3128/squid-internal-mgr/counters | head -10

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Statistics counters displayed (requests, hits, misses, etc.).

### 6. Proxy Functionality Tests

#### 6.1 Test Basic Proxy Functionality
```bash
# Port forward to proxy port
kubectl port-forward -n caching $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test proxy with external request
curl -s --proxy http://localhost:3128 http://httpbin.org/ip

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: JSON response showing IP address, indicating request went through proxy.

#### 6.2 Test Proxy from Within Cluster
```bash
# Create test pod and test proxy
kubectl run test-client --image=curlimages/curl:latest --rm -it -- \
  curl --proxy http://squid.caching.svc.cluster.local:3128 --connect-timeout 10 http://httpbin.org/ip
```

**Expected Result**: JSON response showing external IP, confirming proxy works from within cluster.

### 7. Integration Tests

#### 7.1 Test Metrics Generation After Proxy Usage
```bash
# Generate some proxy traffic
kubectl port-forward -n caching $POD_NAME 3128:3128 &
PF_PROXY_PID=$!
sleep 3

# Make a few requests
curl -s --proxy http://localhost:3128 http://httpbin.org/ip > /dev/null
curl -s --proxy http://localhost:3128 http://httpbin.org/headers > /dev/null

# Kill proxy port forward
kill $PF_PROXY_PID 2>/dev/null || true
sleep 2

# Check if metrics reflect the traffic
kubectl port-forward -n caching $POD_NAME 9301:9301 &
PF_METRICS_PID=$!
sleep 3

# Check request counters
curl -s http://localhost:9301/metrics | grep "squid_client_http_requests_total"

# Cleanup
kill $PF_METRICS_PID 2>/dev/null || true
```

**Expected Result**: Request counters show non-zero values, indicating metrics are being updated.

## Test Summary Checklist

After completing all tests, verify:

- [ ] **Deployment**: Chart installs successfully
- [ ] **Containers**: Both squid and squid-exporter containers running
- [ ] **Service**: Ports 3128 and 9301 accessible
- [ ] **ServiceMonitor**: Created (if Prometheus Operator available)
- [ ] **Metrics**: squid-exporter provides Prometheus metrics
- [ ] **Cache Manager**: Accessible via localhost manager interface
- [ ] **Proxy**: Functions correctly for external requests
- [ ] **Integration**: Metrics update after proxy usage
- [ ] **Cleanup**: All resources removed cleanly

## Quick Test Commands

For rapid testing during development:

```bash
# Quick deployment test
helm install squid ./squid --set cert-manager.enabled=false --wait

# Quick functionality test
kubectl port-forward -n caching svc/squid 3128:3128 &
curl --proxy http://localhost:3128 http://httpbin.org/ip
pkill -f "kubectl port-forward.*3128"

# Quick metrics test
kubectl port-forward -n caching svc/squid 9301:9301 &
curl -s http://localhost:9301/metrics | grep squid_up
pkill -f "kubectl port-forward.*9301"

# Quick cleanup
helm uninstall squid && kubectl delete namespace caching
```
