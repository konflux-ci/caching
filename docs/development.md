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

### Debug Symbols (Optional, for Go Debugging)

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
# Full setup: build, deploy, test
mage all

# Or step by step:
mage kind:up           # Create cluster
mage build:squid       # Build image
mage build:loadSquid   # Load into cluster
mage squidHelm:up      # Deploy chart
mage test:cluster      # Run e2e tests
```

## Manual Setup Steps

### 1. Create Kind Cluster

```bash
kind create cluster --name caching
kubectl cluster-info --context kind-caching
```

### 2. Build and Load Images

```bash
# Build squid image
podman build --target squid -t localhost/konflux-ci/squid:latest -f Containerfile .

# Load into kind
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid:latest)

# Build test image
podman build -t localhost/konflux-ci/squid-test:latest -f test.Containerfile .
kind load image-archive --name caching <(podman save localhost/konflux-ci/squid-test:latest)
```

### 3. Deploy with Helm

```bash
helm install squid ./squid --set environment=dev --set nginx.enabled=true
kubectl get pods -n caching
```

### Helm Configuration Examples

```bash
# Full install with cert-manager
helm install squid ./squid

# Without deploying cert-manager (requires existing cert-manager on cluster)
helm install squid ./squid --set installCertManagerComponents=false

# Fully disable TLS/cert-manager (no SSL-bump, HTTP proxy only)
helm install squid ./squid --set installCertManagerComponents=false --set selfsigned-certificate.enabled=false --set selfsigned-bundle.enabled=false

# Local development
helm install squid ./squid --set environment=dev --set nginx.enabled=true
```

## Cleanup

```bash
mage clean              # Remove cluster + images
# Or manually:
helm uninstall squid
kubectl delete namespace caching
kind delete cluster --name caching
```

## Troubleshooting

See [troubleshooting.md](troubleshooting.md) for common issues and solutions.
