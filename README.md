# Konflux Caching: Squid and NGINX Proxies

This repository provides a Helm chart for two independently configurable caching
proxies in Kubernetes:

| Component | Purpose | Default |
|-----------|---------|---------|
| Squid | Forward proxy with SSL-bump, request caching, and Prometheus metrics | Enabled |
| NGINX | Reverse proxy with response caching, redirect handling, and access-log metrics | Disabled |

Enable or disable them with `squid.enabled` and `nginx.enabled`. The chart manages
the `squid-proxy` and `nginx-proxy` namespaces for the enabled components.

## Prerequisites

> **Quick option**: Use the [dev container](.devcontainer/) for a zero-setup environment with all tools pre-installed.

For manual setup, you need:

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.26.7+ | Required for Mage |
| Podman | Latest | Mage automation uses Podman explicitly |
| Kind | Latest | Kubernetes in Docker/Podman |
| kubectl | Latest | Kubernetes CLI |
| Helm | 3.x | Chart deployment |
| Mage | See `go.mod` | From the repository root, run `go install github.com/magefile/mage` |
| mirrord | Latest | Required only for `mage test:cluster` |

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

This single command creates a Kind cluster, builds images, deploys the Helm chart,
and runs tests. The local Mage deployment enables both proxies; the chart default
enables Squid only.

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
| `mage cachingHelm:up` | Deploy Helm chart |
| `mage cachingHelm:status` | Check deployment |
| `mage test:unit` | Unit tests (no cluster) |
| `mage test:cluster` | E2E tests with mirrord |

## Hermetic Builds

This project uses hermetic (network-isolated) builds in Konflux CI. See [HERMETIC-BUILDS.md](./HERMETIC-BUILDS.md) for guidance on updating dependency lock files.

## Using Squid

Squid is a forward proxy: clients explicitly configure it as their HTTP/HTTPS
proxy. It is enabled by default.

### From Within the Cluster

```bash
# Same namespace
curl --proxy http://squid:3128 http://httpbin.org/ip

# Cross-namespace (replace <NAMESPACE> with your deployment namespace, default: "squid-proxy")
curl --proxy http://squid.<NAMESPACE>.svc.cluster.local:3128 http://httpbin.org/ip
```

#### Testing with a curl Pod

```bash
# Test HTTP proxy
kubectl run test-curl --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl --proxy http://squid.<NAMESPACE>.svc.cluster.local:3128 http://httpbin.org/ip'

# Test HTTPS via SSL-bump (-k to skip cert verification)
kubectl run test-curl-ssl --image=curlimages/curl:latest --rm -it -- \
    sh -c 'curl -k --proxy http://squid.<NAMESPACE>.svc.cluster.local:3128 https://httpbin.org/ip'
```

### From Your Local Machine

```bash
kubectl port-forward -n squid-proxy svc/squid 3128:3128
curl --proxy http://127.0.0.1:3128 http://httpbin.org/ip
```

## Using NGINX

NGINX is a reverse proxy: clients call it directly and it forwards requests to
the configured upstream. Enable it and configure the upstream and cacheable paths:

```yaml
nginx:
  enabled: true
  upstream:
    url: https://upstream.example.com
  cache:
    allowList:
      - ^/artifacts/
```

```bash
helm upgrade --install caching ./caching -f my-values.yaml

# From within the cluster
curl -i http://nginx.nginx-proxy.svc.cluster.local/artifacts/example

# From your local machine
kubectl port-forward -n nginx-proxy svc/nginx 8080:80
curl -i http://127.0.0.1:8080/artifacts/example
```

An empty `nginx.cache.allowList` makes NGINX a pass-through proxy. See
[Redirect Caching Architecture](docs/redirect-caching-architecture.md) for cache,
redirect, authorization, and response-rewrite behavior.

## Helm Configuration

```bash
# Full install with cert-manager (default)
helm install caching ./caching

# Without deploying cert-manager (requires cert-manager already installed on the cluster)
# Note: Certificate resources are still created — cert-manager must be present
helm install caching ./caching --set installCertManagerComponents=false

# Disable Bundle creation and cert-manager installation; certificate resources remain enabled
helm install caching ./caching --set installCertManagerComponents=false --set selfsigned-bundle.enabled=false

# Local development
helm install caching ./caching --set environment=dev --set nginx.enabled=true

# NGINX only
helm install caching ./caching --set squid.enabled=false --set nginx.enabled=true

# Both proxies
helm install caching ./caching --set squid.enabled=true --set nginx.enabled=true
```

### Key Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `environment` | `release` | `dev` (local images), `prerelease`, `release` (Quay) |
| `installCertManagerComponents` | `true` | Deploy cert-manager and trust-manager |
| `selfsigned-bundle.enabled` | `true` | Create trust bundle resource |
| `selfsigned-bundle.name` | `""` | Override Bundle name; empty uses `<squid-namespace>-ca-bundle` |
| `selfsigned-bundle.additionalSources` | `[]` | Additional trust-manager CA sources; Secret sources must exist in the configured trust namespace |
| `selfsigned-certificate.enabled` | `true` | Create certificate resources |
| `squidExporter.enabled` | `true` | Enable Prometheus metrics |
| `perSiteExporter.allowedHostnames` | `[]` | Exact hosts to track individually; all others use `hostname="other"`. See [monitoring](docs/monitoring.md). |
| `squid.enabled` | `true` | Deploy the Squid forward proxy |
| `nginx.enabled` | `false` | Deploy NGINX reverse proxy |
| `nginx.upstream.url` | `""` | Upstream URL; empty uses the chart test backend when `test.enabled=true` |
| `nginx.cache.allowList` | `[]` | Regex paths to cache; empty enables pass-through only |
| `squid.namespace` | `"squid-proxy"` | Namespace for Squid proxy components |
| `nginx.namespace` | `"nginx-proxy"` | Namespace for NGINX reverse proxy components |
| `test.namespace` | `"caching-test"` | Namespace for Helm test, test-server, mirrord, service account, and RBAC resources |
| `clusterDomain` | `""` (defaults to `cluster.local`) | Cluster DNS domain for FQDN generation |

Squid and NGINX deploy into `squid-proxy` and `nginx-proxy` by default. Override
these with `squid.namespace` and `nginx.namespace`. The chart creates the component
namespaces, so do not pass a release namespace with `helm -n`.

## Testing

```bash
# Full test suite via Helm
mage all

# E2E tests with local debugging (mirrord)
mage test:cluster

# Filter tests
GINKGO_LABEL_FILTER='!external-deps' mage test:cluster

# Direct helm test (requires explicit timeout)
helm test caching --timeout=420s
```

The test suite uses [Ginkgo](https://onsi.github.io/ginkgo/) with [mirrord](https://mirrord.dev/) for cluster network access during local development.

For VS Code debugging, test contributions, and advanced options, see [docs/testing.md](docs/testing.md).

## Monitoring

Prometheus monitoring is available for both components:

| Component | Port | Exporter | Metrics |
|-----------|------|----------|---------|
| Squid | 9301 | squid-exporter | Cache stats, request counts, service times |
| Squid | 9302 | per-site-exporter | Per-host request metrics |
| NGINX | 9113 | access-log-exporter | Request and response metrics from access logs |

```bash
# View metrics
kubectl port-forward -n squid-proxy svc/squid 9301:9301
curl http://localhost:9301/metrics

kubectl port-forward -n nginx-proxy svc/nginx 9113:9113
curl http://localhost:9113/metrics
```

For detailed configuration, see [docs/monitoring.md](docs/monitoring.md).

## Troubleshooting

### Quick Diagnostics

```bash
kubectl get pods -n squid-proxy
kubectl logs -n squid-proxy -l app.kubernetes.io/name=squid -c squid
kubectl get pods -n nginx-proxy
kubectl logs -n nginx-proxy -l app.kubernetes.io/name=nginx -c nginx
kubectl logs -n nginx-proxy -l app.kubernetes.io/name=nginx -c access-log-exporter
mage cachingHelm:status
```

### Common Issues

| Issue | Solution |
|-------|----------|
| Cluster exists error | `kind export kubeconfig --name caching` |
| Image pull errors | `mage build:loadSquid` |
| Namespace errors | `helm uninstall caching && kubectl delete ns caching-test squid-proxy nginx-proxy` |
| Connection refused | Check squid ACLs cover your pod CIDR |

For detailed troubleshooting, see [docs/troubleshooting.md](docs/troubleshooting.md).

## Cleanup

```bash
# Automated (recommended)
mage clean

# Manual
helm uninstall caching
kubectl delete namespace caching-test squid-proxy nginx-proxy
kind delete cluster --name caching
```

## Chart Structure

```
caching/
├── Chart.yaml                   # Chart metadata
├── values.yaml                  # Squid, NGINX, and shared configuration
├── crds/                        # Custom Resource Definitions
└── templates/
    ├── configmap.yaml           # Squid configuration
    ├── deployment.yaml          # Squid StatefulSet
    ├── service.yaml             # Squid services
    ├── nginx-configmap.yaml     # NGINX configuration
    ├── nginx-statefulset.yaml   # NGINX StatefulSet
    ├── nginx-service.yaml       # NGINX service
    ├── *servicemonitor.yaml     # Prometheus ServiceMonitors
    └── test-*.yaml              # Helm test infrastructure
```

## Security

- Squid runs as non-root (UID 1001), restricts clients to private networks, and
  blocks unsafe ports and protocols.
- NGINX runs as non-root with dropped capabilities and a read-only root filesystem.
- Both proxies use PVC-backed disk caches (1 GiB by default).

## Contributing

1. Test changes locally with Kind (`mage all`)
2. Verify the proxy works both within and across namespaces
3. Check that cleanup procedures work correctly
4. Update documentation if adding new features
5. Ensure tests pass before submitting PRs
6. Follow conventional commits format

For architecture decisions, see [ADR/](ADR/).

## Documentation

| Document | Description |
|----------|-------------|
| [docs/development.md](docs/development.md) | Development setup and prerequisites |
| [docs/testing.md](docs/testing.md) | Testing guide, VS Code integration, debugging |
| [docs/monitoring.md](docs/monitoring.md) | Prometheus monitoring configuration |
| [docs/troubleshooting.md](docs/troubleshooting.md) | Common issues and solutions |
| [docs/monitoring-tests.md](docs/monitoring-tests.md) | Step-by-step QA testing procedures |
| [docs/redirect-caching-architecture.md](docs/redirect-caching-architecture.md) | NGINX redirect caching architecture and configuration |
| [HERMETIC-BUILDS.md](HERMETIC-BUILDS.md) | Hermetic build configuration |
| [ADR/](ADR/) | Architecture Decision Records |

## License

This project is licensed under the terms specified in the LICENSE file.
