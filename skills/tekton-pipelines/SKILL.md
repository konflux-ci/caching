---
name: tekton-pipelines
description: Use when understanding CI/CD pipelines, troubleshooting builds, or modifying Tekton configurations
---

# Tekton Pipelines

## Overview

This repo uses **Konflux/Tekton** for CI/CD. Pipelines are in `.tekton/` and triggered by Pipelines-as-Code.

## Pipeline Files

| File | Trigger | What it builds |
|------|---------|----------------|
| `squid-pull-request.yaml` | PR to main, merge queue | Squid image (expires 5d) |
| `squid-push.yaml` | Push to main | Squid image (permanent) |
| `squid-tester-pull-request.yaml` | PR to main, merge queue | Test image (expires 5d) |
| `squid-tester-push.yaml` | Push to main | Test image (permanent) |
| `access-log-exporter-pull-request.yaml` | PR to main, merge queue | Sidecar image (expires 5d) |
| `access-log-exporter-push.yaml` | Push to main | Sidecar image (permanent) |
| `squid-helm-pull-request.yaml` | PR to main, merge queue | Helm chart artifact (expires 5d) |
| `squid-helm-push.yaml` | Push to main | Helm chart artifact (permanent) |
| `squid-e2e-eaas-test.yaml` | Manual/integration | E2E on ephemeral OpenShift |

## Trigger Conditions (CEL)

**Pull Request pipelines:**
```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && target_branch == "main" ||
  event == "push" && target_branch.startsWith("gh-readonly-queue/main/")
```

**Push pipelines:**
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "push" && target_branch == "main"
```

## Image Registries

| Stage | Registry |
|-------|----------|
| PR builds | `quay.io/redhat-user-workloads/konflux-vanguard-tenant/caching/...` |
| Push builds | `quay.io/redhat-user-workloads/konflux-vanguard-tenant/caching/...` (same) |

All PR images have `image-expires-after: 5d`. Push images do not expire.

**Note:** Promotion to `quay.io/konflux-ci/caching/...` is handled externally by Konflux Release configuration, not in these pipeline files.

## Key Pipeline Parameters

Common parameters in pipeline specs:

```yaml
- name: git-url
- name: revision
- name: output-image
- name: dockerfile (usually "Containerfile")
- name: path-context (usually ".")
- name: hermetic ("false" for most)
- name: prefetch-input (gomod + rpm for squid)
```

## EaaS (Ephemeral Environment) Testing

`squid-e2e-eaas-test.yaml` runs e2e tests on an ephemeral OpenShift 4.17 cluster.

It expects a **SNAPSHOT** JSON parameter with component images:
```json
{
  "components": [
    {"name": "squid", "containerImage": "..."},
    {"name": "squid-tester", "containerImage": "..."}
  ]
}
```

## Auto-Release Label

Several pipelines have:
```yaml
release.appstudio.openshift.io/auto-release: "false"
```

This prevents automatic promotion. Manual release process required.

## Image Promotion

**Important:** Promotion from workload registry to `quay.io/konflux-ci/caching/` is **NOT configured in this repo**. That's handled by Konflux Release configuration (outside this codebase).

## Hermetic Builds

See `HERMETIC-BUILDS.md` for details on:
- `prefetch-input` for gomod and rpm dependencies
- Lock files: `go.sum`, `rpms.lock.yaml`
- Network isolation during builds

## Common Issues

**Pipeline not triggering**
- Check CEL expression matches your event type
- Verify branch name matches pattern
- Check Pipelines-as-Code controller logs

**Image build fails**
- Check Containerfile syntax
- Verify prefetch dependencies are correct
- Check hermetic build constraints

**EaaS test fails**
- Verify SNAPSHOT JSON format
- Check both squid and squid-tester images exist
- Review test pod logs in ephemeral cluster

## GitHub Actions

`.github/workflows/devcontainer-ci.yml` provides additional CI:
- Triggers: PRs, merge queue, push to main
- Runs in devcontainer (privileged, with Podman socket mounted)
- Steps: `helm lint squid` → `hadolint` (3 Containerfiles) → `mage test:unit` → codecov → `mage squidHelm:up` → `mage test:cluster` → `mage all` → `mage clean`
