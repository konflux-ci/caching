# Hermetic Builds Maintenance Guide

This project uses Cachi2 to enable hermetic (network-isolated) container builds in Konflux CI. Hermetic builds ensure reproducibility and supply chain security by pre-fetching all dependencies before the build runs with network access disabled.

## Overview

In hermetic mode, all dependencies must be declared in advance and version-locked. The build process runs with `--network none`, preventing any runtime dependency downloads. This approach provides reproducible builds with verifiable Software Bills of Materials (SBOMs).

## Prerequisites

The following tools are required for maintaining hermetic builds:

- **[rpm-lockfile-prototype](https://github.com/containerbuildsystem/rpm-lockfile-prototype)** - Generates RPM dependency lock files
- **[Go](https://golang.org/)** - Required for Go module management (`go mod tidy`)


Install rpm-lockfile-prototype:
```bash
pip install rpm-lockfile-prototype
```

## Dependency Lock Files

The following files control which dependencies are available during hermetic builds:

| File | Purpose | Update Frequency |
|------|---------|------------------|
| `artifacts.lock.yaml` | Version locks for external artifacts (currently empty) | Rarely (only if external downloads needed) |
| `rpms.in.yaml` | Declares required RPM packages (including go-toolset) | When adding system packages |
| `rpms.lock.yaml` | Auto-generated transitive RPM dependencies | After changing `rpms.in.yaml` |
| `go.mod` / `go.sum` | Go module dependencies | When adding/updating Go modules |
| `tools.go` | Build-time Go tool dependencies | When adding build tools |
| `ubi-10.repo` | RPM repository definitions | Rarely (only if UBI repos change) |

## Common Maintenance Tasks

### Upgrading Go Version

Go is installed via the `go-toolset` RPM package from UBI10 repositories.

**Automatic updates:** Mintmaker tracks the latest version of `go-toolset` available in UBI10 repositories and will automatically update `rpms.lock.yaml` when new versions are released. No manual intervention is typically required.

**Manual upgrade (if needed):** To force an immediate update to the latest available version:

```bash
rpm-lockfile-prototype --image <BASE_IMAGE_WITH_DIGEST> --outfile rpms.lock.yaml rpms.in.yaml
```

The `rpms.in.yaml` file specifies only the package name without a version, allowing Mintmaker to automatically track the latest available version.

### Adding RPM Packages

When you need to install a new system package, add it to `rpms.in.yaml` and regenerate the lock file.

**Step 1:** Add the package to `rpms.in.yaml`.

```yaml
packages:
  - squid-6.10-5.el10
  - tar
  - jq  # New package
```

**Step 2:** Regenerate the lock file using rpm-lockfile-prototype.

```bash
rpm-lockfile-prototype --image <BASE_IMAGE_WITH_DIGEST> --outfile rpms.lock.yaml rpms.in.yaml
```

Important: Replace `<BASE_IMAGE_WITH_DIGEST>` with the exact image and digest from your Containerfile's `FROM` line. For example, if your Containerfile has `FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:abc123...`, use that complete string in the command above.

**Step 3:** Commit both files.


### Adding Build Tools

When adding a new build-time tool, declare it in `tools.go` and update the Containerfile to build it.

**Step 1:** Add the import to `tools.go`.

```go
import (
    _ "github.com/boynux/squid-exporter"
    _ "github.com/onsi/ginkgo/v2/ginkgo"
    _ "github.com/golangci/golangci-lint/cmd/golangci-lint"  // New tool
)
```

**Step 2:** Update Go dependencies.

```bash
go mod tidy
```

**Step 3:** Add a build step in the Containerfile.

```dockerfile
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    go build -o /usr/local/bin/golangci-lint github.com/golangci/golangci-lint/cmd/golangci-lint
```

## Automated Dependency Updates

- **Go modules** (including Helm): Updated automatically by Renovate
- **RPM packages** (including go-toolset): Updated automatically by Mintmaker

No manual intervention is required for routine dependency updates.

## Troubleshooting

### Build Fails with Network Unreachable Error

This error indicates a missing dependency in the lock files. Examine the error message to identify the missing package or module, add it to the appropriate lock file, and regenerate `rpms.lock.yaml` if needed.

### Conforma Policy Failure for RPM Repositories

This error occurs when repository IDs in `ubi-10.repo` do not match the policy's allowed list. The allowed repositories are defined in https://github.com/release-engineering/rhtap-ec-policy/blob/main/data/known_rpm_repositories.yml

Verify that your repository IDs in `ubi-10.repo` match the format in that file, including architecture suffixes like `x86_64`. For example:
- `ubi-10-baseos-rpms-x86_64` (correct format)
- `ubi-10-baseos-rpms` (may fail if suffix is required)

### Test Dependencies Missing During Build

Test-only dependencies must be explicitly declared in `tools.go`. If tests fail due to missing dependencies, add the required imports to `tools.go` and run `go mod tidy`.

## Best Practices

Always regenerate `rpms.lock.yaml` after modifying `rpms.in.yaml`. The lock file contains transitive dependencies with checksums and should never be edited manually.

Version-lock critical RPM packages for maximum reproducibility. Always verify checksums from official sources when updating `artifacts.lock.yaml`. This ensures the integrity of downloaded toolchain components.

## Summary

Manual intervention may be required for:

- Adding new system packages (RPMs)
- Adding new build-time tools

Go and Helm versions are automatically updated by Mintmaker and Renovate respectively, so manual upgrades are rarely needed. When manual updates are necessary, they require modifying the appropriate lock files and, in the case of RPMs, regenerating the dependency lock file.

## References

- [Cachi2 Documentation](https://github.com/containerbuildsystem/cachi2)
- [Konflux Hermetic Builds](https://konflux-ci.dev/docs/how-tos/hermetic/)
- [Enterprise Contract Policies](https://github.com/enterprise-contract/ec-policies)

