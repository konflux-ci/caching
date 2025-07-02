# Squid Proxy for Kubernetes

This repository contains a Helm chart for deploying a Squid HTTP proxy server in Kubernetes. The chart is designed to be self-contained and deploys into a dedicated `proxy` namespace.

## Prerequisites

### Option 1: Manual Installation

- [Docker](https://docs.docker.com/get-docker/) or [Podman](https://podman.io/getting-started/installation)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (Kubernetes in Docker)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) v3.x

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

## Working with Existing kind Clusters

If you're using the dev container and already have a kind cluster running on your host system, you'll need to configure kubectl access from within the container:

### Check for Existing Clusters

```bash
# List existing kind clusters (this works from within the dev container)
kind get clusters
```

### Configure kubectl Access

If you see an existing cluster (e.g., `kind`), you need to update the kubeconfig:

```bash
# Export the kubeconfig for your existing cluster
kind export kubeconfig --name kind

# Verify access
kubectl cluster-info --context kind-kind
kubectl get nodes
```

### Alternative: Create a New Cluster

If you prefer to start fresh or don't have an existing cluster:

```bash
# Create a new kind cluster
kind create cluster --name kind

# The kubeconfig will be automatically configured
kubectl cluster-info
```

## Quick Start

### 1. Create a kind Cluster

```bash
# Create a new kind cluster
kind create cluster --name kind

# Verify the cluster is running
kubectl cluster-info --context kind-kind
```

### 2. Build and Load the Squid Container Image

```bash
# Build the container image
podman build -t konflux-ci/squid -f Containerfile .

# Load the image into kind using process substitution
kind load image-archive --name kind <(podman save localhost/konflux-ci/squid:latest)
```

### 3. Deploy Squid with Helm

```bash
# Install the Helm chart
helm install squid ./squid

# Verify deployment
kubectl get pods -n proxy
kubectl get svc -n proxy
```

## Using the Proxy

### From Within the Cluster

The Squid proxy is accessible at:

- **Same namespace**: `http://squid:3128`
- **Cross-namespace**: `http://squid.proxy.svc.cluster.local:3128`

#### Example: Testing with a curl pod

```bash
# Create a test pod for testing the proxy
kubectl run test-client --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl --proxy http://squid.proxy.svc.cluster.local:3128 http://httpbin.org/ip'
```

### From Your Local Machine (for testing)

```bash
# Forward the proxy port to your local machine
export POD_NAME=$(kubectl get pods --namespace proxy -l "app.kubernetes.io/name=squid,app.kubernetes.io/instance=squid" -o jsonpath="{.items[0].metadata.name}")
kubectl --namespace proxy port-forward $POD_NAME 3128:3128

# In another terminal, test the proxy
curl --proxy http://127.0.0.1:3128 http://httpbin.org/ip
```

## Configuration

### Squid Configuration

The Squid configuration is stored in `squid/squid.conf` and mounted via a ConfigMap. Key settings include:

- **Allowed networks**: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (covers most Kubernetes pod networks)
- **Port**: 3128
- **Access control**: Allows traffic from local networks, denies unsafe ports

To modify the configuration:

1. Edit `squid/squid.conf`
2. Upgrade the deployment: `helm upgrade squid ./squid`

### Helm Values

Key configuration options in `squid/values.yaml`:

```yaml
replicaCount: 1
image:
  repository: localhost/konflux-ci/squid
  tag: "latest"
namespace:
  name: proxy  # Target namespace
service:
  port: 3128
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
kind export kubeconfig --name kind
kubectl cluster-info --context kind-kind

# Option 2: Delete and recreate
kind delete cluster --name kind
kind create cluster --name kind
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
kind export kubeconfig --name kind

# Switch to the kind context if needed
kubectl config use-context kind-kind

# Test connectivity
kubectl get pods --all-namespaces
```

#### 3. Image Pull Errors

**Symptom**: Pod shows `ImagePullBackOff` or `ErrImagePull`

**Solution**: Ensure the image is loaded into kind:
```bash
# Check if image is loaded
docker exec -it kind-control-plane crictl images | grep squid

# If missing, reload the image
kind load image-archive --name kind <(podman save localhost/konflux-ci/squid:latest)
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

### Debugging Commands

```bash
# Check pod status
kubectl get pods -n proxy

# View pod logs
kubectl logs -n proxy deployment/squid

# Check Squid access logs
kubectl exec -n proxy deployment/squid -- tail -f /var/log/squid/access.log

# Test connectivity from within cluster
kubectl run debug --image=curlimages/curl:latest --rm -it -- curl -v --proxy http://squid.proxy.svc.cluster.local:3128 http://httpbin.org/ip

# Check service endpoints
kubectl get endpoints -n proxy
```

## Monitoring

### Viewing Squid Logs

```bash
# Main Squid logs (startup, errors)
kubectl logs -n proxy deployment/squid

# Access logs (HTTP requests)
kubectl exec -n proxy deployment/squid -- tail -f /var/log/squid/access.log

# Follow logs in real-time
kubectl logs -n proxy deployment/squid -f
```

### Health Checks

The deployment includes TCP-based liveness and readiness probes on port 3128. You may see health check entries in the access logs as:

```
error:transaction-end-before-headers
```

This is normal - Kubernetes is performing TCP health checks without sending complete HTTP requests.

## Cleanup

### Remove the Deployment

```bash
# Uninstall the Helm release
helm uninstall squid

# Remove the namespace (optional, will be recreated on next install)
kubectl delete namespace proxy
```

### Remove the kind Cluster

```bash
# Delete the entire cluster
kind delete cluster --name kind
```

### Clean Up Local Images

```bash
# Remove the local container image
podman rmi localhost/konflux-ci/squid:latest
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

## License

This project is licensed under the terms specified in the LICENSE file.
