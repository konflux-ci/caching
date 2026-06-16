# Development Setup

This guide covers setting up a local development environment for the caching proxy.

## Prerequisites

### System Requirements

Increase the `inotify` resource limits to avoid Kind issues related to [too many open files](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files):

```bash
sudo sysctl fs.inotify.max_user_watches=1048576
sudo sysctl fs.inotify.max_user_instances=1024
```

To make permanent, add to `/etc/sysctl.conf`:
```
fs.inotify.max_user_watches=1048576
fs.inotify.max_user_instances=1024
```

> **Dev Container note**: When using Dev Containers, the automation will fetch these values and verify them against the limits above, failing container initialization if either value is too low.

## Option 1: Manual Installation

### Required Tools

- [Go](https://golang.org/doc/install) 1.25 or later
- [Podman](https://podman.io/getting-started/installation) (required for Mage automation)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (Kubernetes in Docker)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) v3.x
- [Mage](https://magefile.org/) - `go install github.com/magefile/mage@v1.16.1`
- [mirrord](https://mirrord.dev/docs/overview/quick-start/) (optional, for e2e tests)
- [gcc](https://gcc.gnu.org/) (for CGO)

> **Note**: Mage automation uses Podman explicitly. Docker may work for manual commands but is not tested with automation.

### Debug Symbols (Required for Go Debugging)

```bash
# Fedora/RHEL
sudo dnf install -y dnf-plugins-core
sudo dnf --enablerepo=fedora-debuginfo,updates-debuginfo install -y \
    glibc-debuginfo \
    gcc-debuginfo \
    libgcc-debuginfo
```

## Option 2: Development Container

The repository includes a dev container with all tools pre-installed.

### Host Requirements

1. **Podman** installed and running
2. **Docker alias**: The VS Code Dev Containers extension requires the `docker` command:
   - **Fedora Workstation**: `sudo dnf install podman-docker`
   - **Fedora Silverblue/Kinoite**: `sudo rpm-ostree install podman-docker`
   - **Manual**: `sudo ln -s /usr/bin/podman /usr/local/bin/docker`

3. **Podman socket** running:
   ```bash
   systemctl --user enable --now podman.socket
   ```

4. **VS Code** with [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)

### Usage

1. Open the repo in VS Code
2. Use "Reopen in Container" command
3. All tools are pre-configured

## Quick Start

Once prerequisites are met:

```bash
# Full setup: build, deploy, test (ends with helm test)
mage all

# Or step by step:
mage kind:up           # Create cluster
mage build:squid       # Build image
mage build:loadSquid   # Load into cluster
mage squidHelm:up      # Deploy chart
mage test:cluster      # Run e2e tests
```

### Verify Your Setup

After deploying, confirm the proxy is working:

```bash
# From your local machine (port-forward)
kubectl port-forward -n caching svc/squid 3128:3128
curl --proxy http://127.0.0.1:3128 http://httpbin.org/ip

# From within the cluster (creates a temporary test pod)
kubectl run test-curl --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl --proxy http://squid.caching.svc.cluster.local:3128 http://httpbin.org/ip'

# Test HTTPS via SSL-bump
kubectl run test-curl-ssl --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl -k --proxy http://squid.caching.svc.cluster.local:3128 https://httpbin.org/ip'
```

## Mage Command Reference

Run `mage -l` for the full list. Key commands:

| Command | Description |
|---------|-------------|
| **Cluster** | |
| `mage kind:up` | Create/connect to cluster |
| `mage kind:down` | Remove cluster |
| `mage kind:status` | Check cluster status |
| `mage kind:upClean` | Force recreate cluster |
| **Images** | |
| `mage build:squid` | Build squid image (squid + squid-exporter + per-site-exporter + store-id + icap-server) |
| `mage build:loadSquid` | Load squid image into cluster |
| `mage build:accessLogExporter` | Build access-log-exporter image (for use as sidecar with nginx) |
| `mage build:loadAccessLogExporter` | Load access-log-exporter into cluster |
| **Deployment** | |
| `mage squidHelm:up` | Deploy/upgrade helm chart |
| `mage squidHelm:down` | Remove deployment |
| `mage squidHelm:status` | Check deployment status |
| `mage squidHelm:upClean` | Force redeploy |
| **Testing** | |
| `mage test:unit` | Run unit tests (no cluster) |
| `mage test:cluster` | Run E2E tests with mirrord |
| `mage test:clusterMultiReplica` | Run E2E tests with multiple replicas |
| **Linting** | |
| `mage lint:go` | Run golangci-lint (installs pinned version into `bin/`) |
| **Cleanup** | |
| `mage clean` | Remove everything |

### Automation Benefits

- **Dependency management**: Consistent setup without manual version tracking
- **Idempotency**: Commands like `kind:up` are safe to run multiple times
- **Consistent patterns**: `up`/`down`/`status`/`upClean` across targets
- **Single-command setup**: `mage all` handles complete environment
- **Cleanup ordering**: `mage clean` removes resources in correct order

## Linting

Lint checks run in dedicated GitHub Actions workflows (faster feedback than bundling them in the devcontainer job).

| Check | Local | CI workflow |
|-------|-------|-------------|
| golangci-lint | `mage lint:go` | `.github/workflows/go-lint.yaml` |
| Helm chart | `helm lint ./caching` | `.github/workflows/helm-lint.yaml` |
| Containerfiles | — | `.github/workflows/hadolint.yaml` |
| Shell scripts (diff only) | — | `.github/workflows/differential-shellcheck.yaml` |

Before committing, run `mage test:unit`. Helm and Containerfile linting is enforced on pull requests automatically.

## Manual Setup Steps

### 1. Create Kind Cluster

```bash
kind create cluster --name caching
kubectl cluster-info --context kind-caching
```

### 2. Build and Load Images

```bash
# Build squid image (includes squid-exporter, per-site-exporter, store-id, icap-server)
podman build --target squid -t localhost/konflux-ci/squid:latest -f Containerfile .

# Load into kind
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid:latest)

# Build access-log-exporter image (minimal image for use as sidecar with nginx monitoring)
podman build --target access-log-exporter -t localhost/konflux-ci/access-log-exporter:latest -f Containerfile .
kind load image-archive --name caching <(podman save localhost/konflux-ci/access-log-exporter:latest)

# Build test image
podman build -t localhost/konflux-ci/squid-test:latest -f test.Containerfile .
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid-test:latest)
```

### 3. Deploy with Helm

```bash
# Deploy for local dev — enables nginx reverse proxy (required for access-log-exporter sidecar)
helm install squid ./caching --set environment=dev --set nginx.enabled=true
kubectl get pods -n caching
```

### Helm Configuration Examples

```bash
# Full install with cert-manager
helm install squid ./caching

# Without deploying cert-manager (requires cert-manager already installed on the cluster)
# Note: Certificate resources are still created — cert-manager must be present
helm install squid ./caching --set installCertManagerComponents=false

# Install cert-manager + trust-manager without creating certificate resources
helm install squid ./caching --set selfsigned-issuer.enabled=false

# Disable TLS certificate resources (cert-manager still deployed)
helm install squid ./caching --set installCertManagerComponents=false --set selfsigned-bundle.enabled=false

# Local development
helm install squid ./caching --set environment=dev --set nginx.enabled=true
```

## Cleanup

```bash
mage clean              # Remove cluster + images (recommended)
```

### Manual Cleanup

```bash
# Remove Helm release and namespace
helm uninstall squid
kubectl delete namespace caching

# Remove cert-manager namespace (if installed via chart)
kubectl delete namespace cert-manager

# Delete Kind cluster
kind delete cluster --name caching

# Remove local images (optional)
podman rmi localhost/konflux-ci/squid:latest
podman rmi localhost/konflux-ci/access-log-exporter:latest
podman rmi localhost/konflux-ci/squid-test:latest
```

> **Note**: `mage clean` handles most cleanup automatically. Manual image removal is only needed if you want to reclaim disk space.

## Hermetic Builds

This project uses hermetic (network-isolated) builds in Konflux CI to ensure reproducible, supply-chain-secure container images. Dependencies are pre-fetched and version-locked before builds run with network access disabled.

See [HERMETIC-BUILDS.md](../HERMETIC-BUILDS.md) for guidance on updating dependency lock files.

## Troubleshooting

See [troubleshooting.md](troubleshooting.md) for common issues and solutions.
