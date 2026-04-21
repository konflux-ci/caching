# Troubleshooting

This guide covers common issues and their solutions when working with the caching proxy.

## Common Issues

### 1. Cluster Already Exists Error

**Symptom**: When trying to create a kind cluster:
```
ERROR: failed to create cluster: node(s) already exist for a cluster with the name "kind"
```

**Solution**: Either use the existing cluster or delete it first:
```bash
# Option 1: Use existing cluster (recommended for dev container users)
kind export kubeconfig --name caching
kubectl cluster-info --context kind-caching

# Option 2: Delete and recreate
kind delete cluster --name caching
kind create cluster --name caching
```

### 2. kubectl Access Issues (Dev Container)

**Symptom**: `kubectl` commands fail with connection errors when using the dev container

**Solution**: Configure kubectl access to the existing cluster:
```bash
# Check current context
kubectl config current-context

# List available contexts
kubectl config get-contexts

# Export kubeconfig for existing cluster
kind export kubeconfig --name caching

# Switch to the kind context if needed
kubectl config use-context kind-caching

# Test connectivity
kubectl get pods --all-namespaces
```

### 3. Image Pull Errors

**Symptom**: Pod shows `ImagePullBackOff` or `ErrImagePull`

**Solution**: Ensure the image is loaded into kind:
```bash
# Check if image is loaded
docker exec -it caching-control-plane crictl images | grep squid

# If missing, reload the image
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid:latest)
```

### 4. Permission Denied Errors

**Symptom**: Pod logs show `Permission denied` when accessing `/etc/squid/squid.conf`

**Solution**: This is usually resolved by the correct security context in our chart. Verify:
```bash
kubectl describe pod -n caching $(kubectl get pods -n caching -o name | head -1)
```

Look for:
- `runAsUser: 1001`
- `runAsGroup: 0`
- `fsGroup: 0`

### 5. Namespace Already Exists Errors

**Symptom**: Helm install fails with namespace ownership errors

**Solution**: Clean up and reinstall:
```bash
helm uninstall squid 2>/dev/null || true
kubectl delete namespace caching 2>/dev/null || true
# Wait a few seconds for cleanup
sleep 5
helm install squid ./squid
```

### 6. Connection Refused from Pods

**Symptom**: Pods cannot connect to the proxy

**Solution**: Check if the pod network CIDR is covered by Squid's ACLs:
```bash
# Check cluster CIDR
kubectl cluster-info dump | grep -i cidr

# Verify it's covered by the localnet ACLs in squid.conf
# Default ACLs cover: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
```

### 7. Test Pod IP Not Available

**Symptom**: Tests fail with "POD_IP environment variable not set"

**Solution**: Ensure downward API is configured (automatically handled by Helm chart):
```bash
kubectl describe pod -n caching <test-pod-name>
```

### 8. Mirrord Connection Issues

**Symptom**: `mage test:cluster` fails with mirrord connection errors

**Solution**: Verify mirrord infrastructure is deployed and working:
```bash
# Verify mirrord target pod is ready
kubectl get pods -n caching -l app.kubernetes.io/component=mirrord-target

# Check mirrord target pod logs
kubectl logs -n caching mirrord-test-target

# Verify mirrord configuration
cat .mirrord/mirrord.json

# Ensure mirrord is installed
which mirrord
```

### 9. Test Failures

**Symptom**: Tests fail unexpectedly or show connection issues

**Solution**: Debug test execution and cluster state:
```bash
# Run tests with verbose output
mage test:cluster  # Check output for detailed error messages

# Verify cluster state before running tests
mage squidHelm:status

# Check if all pods are running
kubectl get pods -n caching

# Verify proxy connectivity manually
kubectl run debug --image=curlimages/curl:latest --rm -it -- \
  curl -v --proxy http://squid.caching.svc.cluster.local:3128 http://httpbin.org/ip

# View test logs from helm tests
kubectl logs -n caching -l app.kubernetes.io/component=test
```

### 10. Working with Existing kind Clusters (Dev Container Users)

**Symptom**: You're using the dev container and have an existing kind cluster with a different name

**Solution**: Either use the existing cluster or create the expected one:
```bash
# Option 1: Check what clusters exist
kind get clusters

# Option 2: Export kubeconfig for existing cluster (if using default 'kind' cluster)
kind export kubeconfig --name kind
kubectl config use-context kind-kind

# Option 3: Create the expected 'caching' cluster for consistency with automation
kind create cluster --name caching
```

## Debugging Commands

```bash
# Check pod status
kubectl get pods -n caching

# View pod logs
kubectl logs -n caching deployment/squid

# Test connectivity from within cluster
kubectl run debug --image=curlimages/curl:latest --rm -it -- curl -v --proxy http://squid.caching.svc.cluster.local:3128 http://httpbin.org/ip

# Check service endpoints
kubectl get endpoints -n caching

# Verify test infrastructure (when running tests)
kubectl get pods -n caching -l app.kubernetes.io/component=mirrord-target
kubectl get pods -n caching -l app.kubernetes.io/component=test

# View test logs from helm tests
kubectl logs -n caching -l app.kubernetes.io/component=test
```

## Health Checks

The deployment includes TCP-based liveness and readiness probes on port 3128. You may see health check entries in the access logs as:

```
error:transaction-end-before-headers
```

This is normal - Kubernetes is performing TCP health checks without sending complete HTTP requests.

## Metrics Troubleshooting

### No Metrics Appearing

1. **Check if the squid container is running**:
   ```bash
   kubectl get pods -n caching
   kubectl logs -n caching deployment/squid -c squid-exporter
   ```

2. **Verify cache manager access**:
   ```bash
   # Test from within the pod
   kubectl exec -n caching deployment/squid -c squid-exporter -- \
     curl -s http://localhost:3128/squid-internal-mgr/info
   ```

3. **Check ServiceMonitor (if using Prometheus Operator)**:
   ```bash
   kubectl get servicemonitor -n caching
   kubectl describe servicemonitor -n caching squid
   ```

### Metrics Access Denied

If you see "access denied" errors, ensure that the squid configuration allows localhost manager access. The default configuration should work, but if you've modified the configuration in `squid/templates/configmap.yaml`, make sure these lines are present:

```
http_access allow localhost manager
http_access deny manager
```
