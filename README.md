# Squid Proxy for Kubernetes

This repository contains a Helm chart for deploying a Squid HTTP proxy server in Kubernetes. The chart is designed to be self-contained and deploys into a dedicated `proxy` namespace.

## Deveoplment Prerequisites

Increase the `inotify` resource limits to avoid Kind issues related to
[too many open files in](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files).
To increase the limits temporarily, run the following commands:

```bash
sudo sysctl fs.inotify.max_user_watches=524288
sudo sysctl fs.inotify.max_user_instances=512
```

When using Dev Containers, the automation will fetch those values, and if it's
successful, it will verify them against the limits above and fail container
initialization if either value is too low.

### Option 1: Manual Installation

- [gcc](https://gcc.gnu.org/)
- [Go](https://golang.org/doc/install) 1.21 or later
- [Docker](https://docs.docker.com/get-docker/) or [Podman](https://podman.io/getting-started/installation)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (Kubernetes in Docker)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) v3.x
- [Mage](https://magefile.org/) (for automation - `go install github.com/magefile/mage@latest`)
- [mirrord](https://mirrord.dev/docs/overview/quick-start/) (for local development with cluster network access - optional but recommended)

#### Debug Symbols (Required for Go Debugging)

Install the following debug symbols to enable proper debugging of Go applications:

```bash
# Enable debug repositories and install debug symbols
sudo dnf install -y dnf-plugins-core
sudo dnf --enablerepo=fedora-debuginfo,updates-debuginfo install -y \
    glibc-debuginfo \
    gcc-debuginfo \
    libgcc-debuginfo
```

### Option 2: Development Container (Automated)

This repository includes a dev container configuration that provides a consistent development environment with all prerequisites pre-installed. To use it, you need the following on your local machine:

1. **Podman**: The dev container is based on Podman. Ensure it is installed on your system.
2. **Docker alias for Podman**: The VSCode Dev Containers extension relies on using the "docker" command, so the podman command needs to be aliased to "docker". You can either:
   - **Fedora Workstation**: Install the `podman-docker` package: `sudo dnf install podman-docker`
   - **Fedora Silverblue/Kinoite**: Install via rpm-ostree: `sudo rpm-ostree install podman-docker` (requires reboot)
   - **Manual alias** (any system): `sudo ln -s /usr/bin/podman /usr/local/bin/docker`
3. **VS Code with Dev Containers extension**: For the best experience, use Visual Studio Code with the [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers).
4. **Running Podman Socket**: The dev container connects to your local Podman socket. Make sure the user service is running before you start the container:

    ```bash
    systemctl --user start podman.socket
    ```

    To enable the socket permanently so it starts on boot, you can run:

    ```bash
    systemctl --user enable --now podman.socket
    ```

Once these prerequisites are met, you can open this folder in VS Code and use the "Reopen in Container" command to launch the environment with all tools pre-configured.

## Quick Start

### Using automation

This repository includes complete automation for setting up your local development and testing environment. Use this approach for the easiest setup:

```bash
# Set up the complete local dev/test environment automatically
mage all
```

This single command will:
- Create the 'caching' kind cluster (or connect to existing)
- Build the consolidated squid container image (with integrated squid-exporter)
- Load the image into the cluster
- Deploy the Helm chart with all dependencies
- Verify the deployment status

#### Individual Components

You can also run individual components:

```bash
# Cluster management
mage kind:up          # Create/connect to cluster
mage kind:status      # Check cluster status
mage kind:down        # Remove cluster
mage kind:upClean     # Force recreate cluster

# Image management  
mage build:squid         # Build consolidated squid image (includes integrated squid-exporter)
mage build:loadSquid     # Load squid image into cluster

# Deployment management
mage squidHelm:up     # Deploy/upgrade helm chart
mage squidHelm:status # Check deployment status
mage squidHelm:down   # Remove deployment
mage squidHelm:upClean # Force redeploy

# Testing
mage test:cluster     # Run tests with mirrord cluster networking

# Complete cleanup
mage clean           # Remove everything (cluster, images, etc.)
```

#### List All Available Commands

```bash
# See all available automation commands
mage -l
```

### Manual Setup (Advanced)

If you prefer manual control or want to understand the individual steps:

#### 1. Create a kind Cluster

```bash
# Create a new kind cluster (or use: mage kind:up)
kind create cluster --name caching

# Verify the cluster is running
kubectl cluster-info --context kind-caching
```

#### 2. Build and Load the Squid Container Image

```bash
# Build the container image (or use: mage build:squid)
podman build -t localhost/konflux-ci/squid:latest -f Containerfile .

# Load the image into kind (or use: mage build:loadSquid)
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid:latest)
```

#### 3. Build and Load the "Testing" Container Image

```bash
# Build the container image (or use: mage build:squid)
podman build -t localhost/konflux-ci/squid-test:latest -f test.Containerfile .

# Load the image into kind (or use: mage build:loadSquid)
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid-test:latest)
```

#### 4. Deploy Squid with Helm

```bash
# Install the Helm chart (or use: mage squidHelm:up)
helm install squid ./squid

# Verify deployment (or use: mage squidHelm:status)
kubectl get pods -n proxy
kubectl get svc -n proxy
```

By default: 
- The cert-manager and trust-manager dependencies will be deployed into the cert-manager namespace. 
If you wish to disable these deployments, you can do so by setting the parameter `--set installCertManagerComponents=false`

- The resources (issuer, certificate, bundle) are created
if you wish to disable the resources creation, you can do so by setting the parameter `--set selfsigned-bundle.enabled=false`

#### Examples:

  ```bash
  # Install squid + cert-manager + trust-manager + resources
  helm install squid ./squid
  ```

  ```bash
  # Install squid without cert-manager, without trust-manager and without creating resources
  helm install squid ./squid --set installCertManagerComponents=false --set selfsigned-bundle.enabled=false
  ```

  ```bash
  # Install squid + cert-manager + trust-manager without creating resources
  helm install squid ./squid --set selfsigned-issuer.enabled=false
  ```

  ```bash
  # Install squid without cert-manager, without trust-manager + create resources
  helm install squid ./squid --set installCertManagerComponents=false
  ```

## Using the Proxy

### From Within the Cluster

The Squid proxy is accessible at:

- **Same namespace**: `http://squid:3128`
- **Cross-namespace**: `http://squid.proxy.svc.cluster.local:3128`

#### Example: Testing with a curl pod

```bash
# Create a test pod for testing the proxy (HTTP)
kubectl run test-client --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl --proxy http://squid.proxy.svc.cluster.local:3128 http://httpbin.org/ip'

# Create a test pod for testing SSL-Bump (HTTPS)
kubectl run test-client-ssl --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl -k --proxy http://squid.proxy.svc.cluster.local:3128 https://httpbin.org/ip'
```

### From Your Local Machine (for testing)

```bash
# Forward the proxy port to your local machine
export POD_NAME=$(kubectl get pods --namespace proxy -l "app.kubernetes.io/name=squid,app.kubernetes.io/instance=squid" -o jsonpath="{.items[0].metadata.name}")
kubectl --namespace proxy port-forward $POD_NAME 3128:3128

# In another terminal, test the proxy (HTTP)
curl --proxy http://127.0.0.1:3128 http://httpbin.org/ip
```


## Testing

This repository includes comprehensive end-to-end tests to validate the Squid proxy deployment and HTTP caching functionality. The test suite uses [Ginkgo](https://onsi.github.io/ginkgo/) for behavior-driven testing and [mirrord](https://mirrord.dev/) for local development with cluster network access.

### Quick Start - Run All Tests

```bash
# Complete test setup and execution (recommended)
mage all
```

In this mode tests are invoked by Helm via `helm test`.

### Local Development Testing with Mirrord

For local development and debugging, use mirrord to run tests with cluster network access:

```bash
# Setup test environment
mage squidHelm:up

# Run tests locally with cluster network access
mage test:cluster
```

This uses mirrord to "steal" network connections from a target pod and runs
the test locally (outside of the Kind cluster) with Ginkgo. This allows for 
local debugging without rebuilding test containers

### VS Code Integration

The repository includes complete VS Code configuration for Ginkgo testing:

#### Debug Configurations

Use VS Code's debug panel to run tests with breakpoints:

1. **Debug Ginkgo E2E Tests**: Run all E2E tests with debugging
2. **Debug Ginkgo Tests (Current File)**: Debug tests in the currently open file
3. **Run Ginkgo Tests with Coverage**: Generate test coverage reports

#### Tasks and Commands

Available VS Code tasks (Ctrl+Shift+P → "Tasks: Run Task"):

- **Setup Test Environment**: Runs `mage all` to prepare everything
- **Run Ginkgo E2E Tests**: Execute the full test suite
- **Clean Test Environment**: Clean up all resources
- **Run Focused Ginkgo Tests**: Run specific test patterns

### Contributing to Tests

When adding new tests:

1. **Use test helpers**: Leverage `tests/testhelpers/` for common operations
2. **Follow Ginkgo patterns**: Use `Describe`, `Context`, `It` for clear test structure
3. **Add cache-busting**: Use unique URLs to prevent test interference
4. **Verify cleanup**: Ensure tests clean up resources properly
5. **Update VS Code config**: Add debug configurations for new test files
## Prometheus Monitoring

This chart includes comprehensive Prometheus monitoring capabilities through the integrated [squid-exporter](https://github.com/boynux/squid-exporter) (upstream). The squid-exporter is built into the main Squid container, providing a single, consolidated image. The monitoring system provides detailed metrics about Squid's operational status, including:

- **Liveness**: Squid service information and connection status
- **Bandwidth Usage**: Client HTTP and Server HTTP traffic metrics
- **Hit/Miss Rates**: Cache performance metrics (general and detailed)
- **Storage Utilization**: Cache information and memory usage
- **Service Times**: Response times for different operations
- **Connection Information**: Active connections and request details

### Enabling Monitoring

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

### Prometheus Integration

#### Option 1: Prometheus Operator (Recommended)

If you're using Prometheus Operator, the chart automatically creates a ServiceMonitor:

```yaml
prometheus:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    namespace: ""  # Leave empty to use the same namespace as the app
```

#### Option 2: Manual Prometheus Configuration

**ServiceMonitor CRD Included:**

The chart includes the ServiceMonitor CRD in the `crds/` directory, so it will be automatically installed by Helm when the chart is deployed. This follows the same pattern as the cert-manager and trust-manager CRDs.

If you don't want ServiceMonitor functionality, you can disable it:
```bash
helm install squid ./squid --set prometheus.serviceMonitor.enabled=false
```

**For non-Prometheus Operator setups**, disable ServiceMonitor and use manual Prometheus configuration:

For manual Prometheus setup (if you have an existing Prometheus instance), add this scrape configuration to your external Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'squid-proxy'
    static_configs:
      - targets: ['squid.proxy.svc.cluster.local:9301']
    scrape_interval: 30s
    metrics_path: '/metrics'
```

**Complete example**: See `docs/prometheus-config-example.yaml` in this repository for a full Prometheus configuration file.

### Available Metrics

The monitoring system provides numerous metrics including:

#### Standard Squid Metrics (Port 9301)

- `squid_client_http_requests_total`: Total client HTTP requests
- `squid_client_http_hits_total`: Total client HTTP hits
- `squid_client_http_errors_total`: Total client HTTP errors
- `squid_server_http_requests_total`: Total server HTTP requests
- `squid_cache_memory_bytes`: Cache memory usage
- `squid_cache_disk_bytes`: Cache disk usage
- `squid_service_times_seconds`: Service times for different operations
- `squid_up`: Squid availability status

### Accessing Metrics

#### Via Port Forward

```bash
# Forward the standard squid-exporter metrics port
kubectl port-forward -n proxy svc/squid 9301:9301

# View standard metrics in your browser or with curl
curl http://localhost:9301/metrics
```

#### Via Service

The metrics are exposed on the service:

```bash
# Standard squid-exporter metrics (from within the cluster)
curl http://squid.proxy.svc.cluster.local:9301/metrics
```

### Troubleshooting Metrics

#### No Metrics Appearing

1. **Check if the squid container is running**:
   ```bash
   kubectl get pods -n proxy
   kubectl logs -n proxy deployment/squid -c squid-exporter
   ```

2. **Verify cache manager access**:
   ```bash
   # Test from within the pod
   kubectl exec -n proxy deployment/squid -c squid-exporter -- \
     curl -s http://localhost:3128/squid-internal-mgr/info
   ```

3. **Check ServiceMonitor (if using Prometheus Operator)**:
   ```bash
   kubectl get servicemonitor -n proxy
   kubectl describe servicemonitor -n proxy squid
   ```

#### Metrics Access Denied

If you see "access denied" errors, ensure that the squid configuration allows localhost manager access. The default configuration should work, but if you've modified `squid.conf`, make sure these lines are present:

```
http_access allow localhost manager
http_access deny manager
```

## Troubleshooting

### Common Issues

#### 1. Cluster Already Exists Error

**Symptom**: When trying to create a kind cluster, you see:
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

#### 2. kubectl Access Issues (Dev Container)

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

#### 3. Image Pull Errors

**Symptom**: Pod shows `ImagePullBackOff` or `ErrImagePull`

**Solution**: Ensure the image is loaded into kind:
```bash
# Check if image is loaded
docker exec -it caching-control-plane crictl images | grep squid

# If missing, reload the image
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid:latest)
```

#### 4. Permission Denied Errors

**Symptom**: Pod logs show `Permission denied` when accessing `/etc/squid/squid.conf`

**Solution**: This is usually resolved by the correct security context in our chart. Verify:
```bash
kubectl describe pod -n proxy $(kubectl get pods -n proxy -o name | head -1)
```

Look for:
- `runAsUser: 1001`
- `runAsGroup: 0`
- `fsGroup: 0`

#### 5. Namespace Already Exists Errors

**Symptom**: Helm install fails with namespace ownership errors

**Solution**: Clean up and reinstall:
```bash
helm uninstall squid 2>/dev/null || true
kubectl delete namespace proxy 2>/dev/null || true
# Wait a few seconds for cleanup
sleep 5
helm install squid ./squid
```

#### 6. Connection Refused from Pods

**Symptom**: Pods cannot connect to the proxy

**Solution**: Check if the pod network CIDR is covered by Squid's ACLs:
```bash
# Check cluster CIDR
kubectl cluster-info dump | grep -i cidr

# Verify it's covered by the localnet ACLs in squid.conf
# Default ACLs cover: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
```

#### 7. Test Pod IP Not Available

**Symptom**: Tests fail with "POD_IP environment variable not set"

**Solution**: Ensure downward API is configured (automatically handled by Helm chart):
```bash
kubectl describe pod -n proxy <test-pod-name>
```

#### 8. Mirrord Connection Issues

**Symptom**: `mage test:cluster` fails with mirrord connection errors

**Solution**: Verify mirrord infrastructure is deployed and working:
```bash
# Verify mirrord target pod is ready
kubectl get pods -n proxy -l app.kubernetes.io/component=mirrord-target

# Check mirrord target pod logs
kubectl logs -n proxy mirrord-test-target

# Verify mirrord configuration
cat .mirrord/mirrord.json

# Ensure mirrord is installed
which mirrord
```

#### 9. Test Failures

**Symptom**: Tests fail unexpectedly or show connection issues

**Solution**: Debug test execution and cluster state:
```bash
# Run tests with verbose output
mage test:cluster  # Check output for detailed error messages

# Verify cluster state before running tests
mage squidHelm:status

# Check if all pods are running
kubectl get pods -n proxy

# Verify proxy connectivity manually
kubectl run debug --image=curlimages/curl:latest --rm -it -- \
  curl -v --proxy http://squid.proxy.svc.cluster.local:3128 http://httpbin.org/ip

# View test logs from helm tests
kubectl logs -n proxy -l app.kubernetes.io/component=test
```

#### 10. Working with Existing kind Clusters (Dev Container Users)

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

### Debugging Commands

```bash
# Check pod status
kubectl get pods -n proxy

# View pod logs
kubectl logs -n proxy deployment/squid

# Test connectivity from within cluster
kubectl run debug --image=curlimages/curl:latest --rm -it -- curl -v --proxy http://squid.proxy.svc.cluster.local:3128 http://httpbin.org/ip

# Check service endpoints
kubectl get endpoints -n proxy

# Verify test infrastructure (when running tests)
kubectl get pods -n proxy -l app.kubernetes.io/component=mirrord-target
kubectl get pods -n proxy -l app.kubernetes.io/component=test

# View test logs from helm tests
kubectl logs -n proxy -l app.kubernetes.io/component=test
```

### Health Checks

The deployment includes TCP-based liveness and readiness probes on port 3128. You may see health check entries in the access logs as:

```
error:transaction-end-before-headers
```

This is normal - Kubernetes is performing TCP health checks without sending complete HTTP requests.

## Testing with Squid Proxy Monitoring Integration

This section provides step-by-step testing instructions for the Squid proxy monitoring integration.

### Complete Monitoring Integration Tests

#### 1. Pre-Test Setup

##### 1.1 Verify Cluster Connectivity
```bash
# Test basic cluster access
kubectl cluster-info

# Show current context
kubectl config current-context

# Verify nodes are ready
kubectl get nodes
```

**Expected Result**: Cluster information displays correctly with no errors.

##### 1.2 Install Prometheus Operator (if needed)
```bash
# Check if ServiceMonitor CRD exists
kubectl get crd servicemonitors.monitoring.coreos.com

# If the CRD doesn't exist, install Prometheus Operator
if ! kubectl get crd servicemonitors.monitoring.coreos.com &> /dev/null; then
  echo "Installing Prometheus Operator..."
  kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/bundle.yaml
  
  # Wait for CRDs to be established
  kubectl wait --for condition=established --timeout=60s crd/servicemonitors.monitoring.coreos.com
fi
```

**Expected Result**: ServiceMonitor CRD is available for monitoring integration.

##### 1.3 Clean Previous Deployments
```bash
# Remove any existing deployment
helm uninstall squid 2>/dev/null || true

# Remove namespace
kubectl delete namespace proxy 2>/dev/null || true

# Wait for cleanup
sleep 10
```

**Expected Result**: Clean environment with no conflicts.

#### 2. Deployment Tests

##### 2.1 Deploy Squid with Monitoring
```bash
# Deploy with monitoring enabled
helm install squid ./squid \
  --set squidExporter.enabled=true \
  --set prometheus.serviceMonitor.enabled=true \
  --set cert-manager.enabled=false \
  --wait --timeout=300s
```

**Expected Result**: Deployment succeeds with `STATUS: deployed`.

##### 2.2 Verify Pod Readiness
```bash
# Wait for pods to be ready
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=squid -n proxy --timeout=120s

# Check pod status
kubectl get pods -n proxy -o wide
```

**Expected Result**: Pod shows `2/2 Running` (squid + squid-exporter containers).

##### 2.3 Verify Service Creation
```bash
# Check service
kubectl get svc -n proxy

# Check service details
kubectl describe svc squid -n proxy
```

**Expected Result**: Service exposes ports 3128 (proxy) and 9301 (metrics).

##### 2.4 Verify ServiceMonitor (if Prometheus Operator available)
```bash
# Check ServiceMonitor
kubectl get servicemonitor -n proxy

# Check ServiceMonitor details
kubectl describe servicemonitor squid -n proxy
```

**Expected Result**: ServiceMonitor created with correct selector and endpoints.

#### 3. Container Health Tests

##### 3.1 Verify Container Configuration
```bash
# Get pod name
POD_NAME=$(kubectl get pods -n proxy -l app.kubernetes.io/name=squid -o jsonpath="{.items[0].metadata.name}")
echo "Testing pod: $POD_NAME"

# Check container names
kubectl get pod $POD_NAME -n proxy -o jsonpath='{.spec.containers[*].name}'
```

**Expected Result**: Shows both `squid` and `squid-exporter` containers.

##### 3.2 Check Container Logs
```bash
# Check squid container logs
kubectl logs -n proxy $POD_NAME -c squid --tail=10

# Check squid-exporter container logs
kubectl logs -n proxy $POD_NAME -c squid-exporter --tail=10
```

**Expected Result**: 
- Squid logs show successful startup with no permission errors
- Squid-exporter logs show successful connection to cache manager

#### 4. Metrics Endpoint Tests

##### 4.1 Test Direct Metrics Access
```bash
# Port forward to metrics endpoint
kubectl port-forward -n proxy $POD_NAME 9301:9301 &
PF_PID=$!
sleep 3

# Test metrics endpoint
curl -s http://localhost:9301/metrics | head -20

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Prometheus metrics displayed starting with `# HELP` and `# TYPE` comments.

##### 4.2 Test Service-Based Metrics Access
```bash
# Port forward via service
kubectl port-forward -n proxy svc/squid 9301:9301 &
PF_PID=$!
sleep 3

# Test metrics via service
curl -s http://localhost:9301/metrics | grep -c "^squid_"

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Number of squid metrics found (typically 20+ metrics).

##### 4.3 Verify Specific Metrics
```bash
# Port forward for detailed metrics check
kubectl port-forward -n proxy svc/squid 9301:9301 &
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

#### 5. Cache Manager Tests

##### 5.1 Test Cache Manager Access
```bash
# Port forward to proxy port
kubectl port-forward -n proxy $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test cache manager info
curl -s http://localhost:3128/squid-internal-mgr/info | head -10

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Cache manager information displayed (version, uptime, etc.).

##### 5.2 Test Cache Manager Counters
```bash
# Port forward to proxy port
kubectl port-forward -n proxy $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test cache manager counters
curl -s http://localhost:3128/squid-internal-mgr/counters | head -10

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: Statistics counters displayed (requests, hits, misses, etc.).

#### 6. Proxy Functionality Tests

##### 6.1 Test Basic Proxy Functionality
```bash
# Port forward to proxy port
kubectl port-forward -n proxy $POD_NAME 3128:3128 &
PF_PID=$!
sleep 3

# Test proxy with external request
curl -s --proxy http://localhost:3128 http://httpbin.org/ip

# Cleanup
kill $PF_PID 2>/dev/null || true
```

**Expected Result**: JSON response showing IP address, indicating request went through proxy.

##### 6.2 Test Proxy from Within Cluster
```bash
# Create test pod and test proxy
kubectl run test-client --image=curlimages/curl:latest --rm -it -- \
  curl --proxy http://squid.proxy.svc.cluster.local:3128 --connect-timeout 10 http://httpbin.org/ip
```

**Expected Result**: JSON response showing external IP, confirming proxy works from within cluster.

#### 7. Integration Tests

##### 7.1 Test Metrics Generation After Proxy Usage
```bash
# Generate some proxy traffic
kubectl port-forward -n proxy $POD_NAME 3128:3128 &
PF_PROXY_PID=$!
sleep 3

# Make a few requests
curl -s --proxy http://localhost:3128 http://httpbin.org/ip > /dev/null
curl -s --proxy http://localhost:3128 http://httpbin.org/headers > /dev/null

# Kill proxy port forward
kill $PF_PROXY_PID 2>/dev/null || true
sleep 2

# Check if metrics reflect the traffic
kubectl port-forward -n proxy $POD_NAME 9301:9301 &
PF_METRICS_PID=$!
sleep 3

# Check request counters
curl -s http://localhost:9301/metrics | grep "squid_client_http_requests_total"

# Cleanup
kill $PF_METRICS_PID 2>/dev/null || true
```

**Expected Result**: Request counters show non-zero values, indicating metrics are being updated.

### Test Summary Checklist

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

### Quick Test Commands

For rapid testing during development:

```bash
# Quick deployment test
helm install squid ./squid --set cert-manager.enabled=false --wait

# Quick functionality test
kubectl port-forward -n proxy svc/squid 3128:3128 &
curl --proxy http://localhost:3128 http://httpbin.org/ip
pkill -f "kubectl port-forward.*3128"

# Quick metrics test
kubectl port-forward -n proxy svc/squid 9301:9301 &
curl -s http://localhost:9301/metrics | grep squid_up
pkill -f "kubectl port-forward.*9301"

# Quick cleanup
helm uninstall squid && kubectl delete namespace proxy
```

## Cleanup

### Automated Cleanup (Recommended)

```bash
# Remove everything (cluster, deployments, images) in one command
mage clean
```

### Manual Cleanup (Advanced)

If you prefer manual control:

#### Remove the Deployment

```bash
# Uninstall the Helm release
helm uninstall squid

# Remove the namespaces (optional, will be recreated on next install)
kubectl delete namespace proxy

# If you used the "squid" helm chart to install cert-manager
kubectl delete namespace cert-manager
```

#### Remove the kind Cluster

```bash
# Delete the entire cluster (or use: mage kind:down)
kind delete cluster --name caching
```

#### Clean Up Local Images

```bash
# Remove the local container images
podman rmi localhost/konflux-ci/squid:latest
podman rmi localhost/konflux-ci/squid-test:latest
```

## Chart Structure

```
squid/
├── Chart.yaml              # Chart metadata
├── values.yaml             # Default configuration values
├── squid.conf              # Squid configuration file
└── templates/
    ├── _helpers.tpl         # Template helpers
    ├── configmap.yaml       # ConfigMap for squid.conf
    ├── deployment.yaml      # Squid deployment
    ├── namespace.yaml       # Proxy namespace
    ├── service.yaml         # Squid service
    ├── serviceaccount.yaml  # Service account
    ├── servicemonitor.yaml  # Prometheus ServiceMonitor
    └── NOTES.txt           # Post-install instructions
```

## Security Considerations

- The proxy runs as non-root user (UID 1001)
- Access is restricted to RFC 1918 private networks
- Unsafe ports and protocols are blocked
- No disk caching is enabled by default (memory-only)

## Contributing

When modifying the chart:

1. Test changes locally with kind
2. Update this README if adding new features
3. Verify the proxy works both within and across namespaces
4. Check that cleanup procedures work correctly

## Automation Benefits

The Mage-based automation system provides several key benefits:

- **Dependency Management**: Automatically handles prerequisites (cluster → image → deployment)
- **Error Handling**: Robust error handling with clear feedback
- **Idempotent Operations**: Can run commands multiple times safely
- **Smart Logic**: Detects existing resources and handles install vs upgrade scenarios
- **Consistent Patterns**: All resource types follow the same up/down/status/upClean pattern
- **Single Command Setup**: `mage all` sets up the complete environment
- **Efficient Cleanup**: `mage clean` removes all resources in the correct order

For the best experience, use the automation commands instead of manual setup!

## License

This project is licensed under the terms specified in the LICENSE file.
