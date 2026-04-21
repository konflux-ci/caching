# Squid Proxy for Kubernetes

A Helm chart for deploying a Squid HTTP proxy in Kubernetes, with SSL-bump support, caching, and Prometheus monitoring. Deploys into a dedicated `caching` namespace.

## Prerequisites

> **Quick option**: Use the [dev container](.devcontainer/) for a zero-setup environment with all tools pre-installed.

For manual setup, you need:

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.25+ | Required for Mage |
| Podman | Latest | Mage automation uses Podman explicitly |
| Kind | Latest | Kubernetes in Docker/Podman |
| kubectl | Latest | Kubernetes CLI |
| Helm | 3.x | Chart deployment |
| Mage | 1.16+ | `go install github.com/magefile/mage@latest` |

**Critical for Kind**: Increase inotify limits to avoid [file watcher issues](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files):

```bash
sudo sysctl fs.inotify.max_user_watches=1048576
sudo sysctl fs.inotify.max_user_instances=1024
```

For detailed setup instructions, see [docs/development.md](docs/development.md).

## Quick Start

```bash
# Full setup: build, deploy, test
mage all
```

This single command creates a Kind cluster, builds images, deploys the Helm chart, and runs tests.

### Essential Commands

| Command | Description |
|---------|-------------|
| `mage all` | Complete setup and test |
| `mage clean` | Remove everything |
| `mage -l` | List all commands |

### Individual Components

| Command | Description |
|---------|-------------|
| `mage kind:up` | Create Kind cluster |
| `mage kind:down` | Delete Kind cluster |
| `mage build:squid` | Build Squid image |
| `mage build:loadSquid` | Load image into cluster |
| `mage squidHelm:up` | Deploy Helm chart |
| `mage squidHelm:status` | Check deployment |
| `mage test:unit` | Unit tests (no cluster) |
| `mage test:cluster` | E2E tests with mirrord |

## Hermetic Builds

This project uses hermetic (network-isolated) builds in Konflux CI. See [HERMETIC-BUILDS.md](./HERMETIC-BUILDS.md) for guidance on updating dependency lock files.

## Using the Proxy

### From Within the Cluster

```bash
# Same namespace
curl --proxy http://squid:3128 http://httpbin.org/ip

# Cross-namespace
curl --proxy http://squid.caching.svc.cluster.local:3128 http://httpbin.org/ip
```

### From Your Local Machine

```bash
kubectl port-forward -n caching svc/squid 3128:3128
curl --proxy http://127.0.0.1:3128 http://httpbin.org/ip
```

## Helm Configuration

```bash
# Full install with cert-manager (default)
helm install squid ./squid

# Without cert-manager (requires existing cert-manager on cluster, or disable TLS)
helm install squid ./squid --set installCertManagerComponents=false

# Fully disable TLS/cert-manager (no SSL-bump, HTTP proxy only)
helm install squid ./squid --set installCertManagerComponents=false --set selfsigned-certificate.enabled=false --set selfsigned-bundle.enabled=false

# Local development
helm install squid ./squid --set environment=dev --set nginx.enabled=true
```

### Key Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `environment` | `release` | `dev` (local images), `prerelease`, `release` (Quay) |
| `installCertManagerComponents` | `true` | Deploy cert-manager and trust-manager |
| `selfsigned-bundle.enabled` | `true` | Create self-signed certificates |
| `squidExporter.enabled` | `true` | Enable Prometheus metrics |
| `nginx.enabled` | `false` | Deploy NGINX reverse proxy |

## Testing

```bash
# Full test suite via Helm
mage all

# E2E tests with local debugging (mirrord)
mage test:cluster

# Filter tests
GINKGO_LABEL_FILTER='!external-deps' mage test:cluster
```

The test suite uses [Ginkgo](https://onsi.github.io/ginkgo/) with [mirrord](https://mirrord.dev/) for cluster network access during local development.

## Monitoring

Prometheus monitoring is enabled by default with two exporters:

| Port | Exporter | Metrics |
|------|----------|---------|
| 9301 | squid-exporter | Cache stats, request counts, service times |
| 9302 | per-site-exporter | Per-host request metrics |

```bash
# View metrics
kubectl port-forward -n caching svc/squid 9301:9301
curl http://localhost:9301/metrics
```

For detailed configuration, see [docs/monitoring.md](docs/monitoring.md).

## Troubleshooting

### Quick Diagnostics

```bash
kubectl get pods -n caching
kubectl logs -n caching -l app.kubernetes.io/name=squid -c squid
mage squidHelm:status
```

### Common Issues

| Issue | Solution |
|-------|----------|
| Cluster exists error | `kind export kubeconfig --name caching` |
| Image pull errors | `mage build:loadSquid` |
| Namespace errors | `helm uninstall squid && kubectl delete ns caching` |
| Connection refused | Check squid ACLs cover your pod CIDR |

For detailed troubleshooting, see [docs/troubleshooting.md](docs/troubleshooting.md).

## Cleanup

```bash
# Automated (recommended)
mage clean

# Manual
helm uninstall squid
kubectl delete namespace caching
kind delete cluster --name caching
```

## Chart Structure

```
squid/
├── Chart.yaml          # Chart metadata
├── values.yaml         # Default configuration
├── crds/               # Custom Resource Definitions
└── templates/
    ├── configmap.yaml  # Squid configuration
    ├── deployment.yaml # StatefulSet deployment
    ├── service.yaml    # Service definitions
    └── ...
```

## Security

- Runs as non-root (UID 1001)
- Restricted to RFC 1918 private networks
- Unsafe ports/protocols blocked
- Memory-only caching by default

## Contributing

1. Test changes locally with Kind (`mage all`)
2. Update documentation if adding new features
3. Ensure tests pass before submitting PRs
4. Follow conventional commits format

For architecture decisions, see [ADR/](ADR/).

## Documentation

| Document | Description |
|----------|-------------|
| [docs/development.md](docs/development.md) | Development setup and prerequisites |
| [docs/monitoring.md](docs/monitoring.md) | Prometheus monitoring configuration |
| [docs/troubleshooting.md](docs/troubleshooting.md) | Common issues and solutions |
| [docs/monitoring-tests.md](docs/monitoring-tests.md) | Step-by-step QA testing procedures |
| [HERMETIC-BUILDS.md](HERMETIC-BUILDS.md) | Hermetic build configuration |
| [ADR/](ADR/) | Architecture Decision Records |

## License

This project is licensed under the terms specified in the LICENSE file.
