# 3. Sunset Squid container-image-layer proxy

Date: 2026-10-25

## Status

Accepted

## Context

ADR 0001 introduced Squid with SSL-bump, store-id, and ICAP to cache container image
layers from registry CDNs. ADR 0002 defined the constrained MITM CA needed for that
path.

In practice:

- Use is opt-in (`enable-cache-proxy`, default false); most clusters see little useful
  cache value.
- A/B testing showed higher CDN hit rates do not reduce build wall-clock; SSL-bump adds
  fixed overhead, and remote/multi-arch builds often never use the in-cluster proxy.
- The operational cost (upstream maintenance, deploy, CA trust rotation, exporters) is
  not justified.

## Decision

We will **deprecate and remove** Squid as Konflux’s container-image-layer forward proxy
(`container-image-proxy`).

- Known downstream deployments to stop enabling it in cluster config and stop it from
  being deployed.
- Stop building the Squid image, and retire Squid-only pieces from this repository.
- This ADR **supersedes** ADR 0001. ADR 0002 applies only to the Squid MITM CA and
  becomes historical once Squid is gone.

**Out of scope:** NGINX / `artifact-registry-proxy` (package-registry reverse proxy).

## Consequences

Positive:

- Smaller infra and security surface (no SSL-bump CA for image pulls).
- Simpler default build path: registries/CDNs directly.

Negative:

- No shared in-cluster OCI layer cache for remaining opt-in users.
- Any future layer-caching approach requires a new ADR.
