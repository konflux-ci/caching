# Redirect Caching Architecture

## Overview

The nginx proxy layer intercepts HTTP redirects from upstream registries (e.g., Nexus) and follows them internally, caching the resulting content on disk. Build pods receive artifact content directly (`200`) instead of redirect responses (`302`).

## Request Flow

```mermaid
graph LR
    %% External Nodes
    B["Build Pod"]
    S[("S3 / CDN")]

    %% Subgraph for Nginx Proxy Pod
    subgraph "Nginx Proxy Pod"
        direction TB
        AL["AllowList Location"]
        HR["handle_redirect"]
        DC[("Disk Cache")]

        %% Invisible links for vertical alignment
        AL ~~~ HR ~~~ DC

        %% Internal flow
        HR -->|"Cache response"| DC
    end

    %% Upstream
    N["Nexus"]

    %% --- Primary Flow ---
    B -->|"GET /redirect/artifact"| AL
    AL -->|"Always contacts upstream"| N
    N -->|"302 intercepted"| HR
    HR -->|"Follows redirect"| S
    S -->|"200 content"| HR
    HR -->|"200 content"| B

    %% --- Cache Hit Flow ---
    DC -.->|"Cache HIT"| HR

    %% --- Style Definitions ---
    style B fill:#4A90E2,color:#fff
    style N fill:#F5A623,color:#000
    style S fill:#50E3C2,color:#000
    style AL fill:#BB46B4,color:#fff
    style HR fill:#BB46B4,color:#fff
    style DC fill:#9B9B9B,color:#fff
```

## Cache Miss vs Cache Hit

```mermaid
sequenceDiagram
    participant Build as Build Pod
    participant Nginx as Nginx Proxy
    participant Nexus as Nexus
    participant S3 as S3 / CDN

    Note over Build,S3: First request - cache MISS

    Build->>Nginx: GET /redirect/artifact-1.0.tar
    Nginx->>Nexus: GET /redirect/artifact-1.0.tar
    Nexus-->>Nginx: 302 Location: s3.../artifact

    Note over Nginx: proxy_intercept_errors catches 302<br/>error_page routes to handle_redirect

    Nginx->>S3: GET /artifact
    S3-->>Nginx: 200 artifact content

    Note over Nginx: Caches with key = original URI<br/>not the S3 presigned URL

    Nginx-->>Build: 200 content, X-Cache-Status: MISS

    Note over Build,S3: Second request - cache HIT

    Build->>Nginx: GET /redirect/artifact-1.0.tar
    Nginx->>Nexus: GET /redirect/artifact-1.0.tar
    Nexus-->>Nginx: 302 Location: s3.../artifact

    Note over Nginx: HIT on original URI key<br/>S3 redirect target ignored

    Nginx-->>Build: 200 content, X-Cache-Status: HIT

    Note over Build,S3: Ban enforcement - upstream returns 403

    Build->>Nginx: GET /redirect/artifact-1.0.tar
    Nginx->>Nexus: GET /redirect/artifact-1.0.tar
    Nexus-->>Nginx: 403 Forbidden

    Note over Nginx: Non-redirect responses pass through<br/>Cache is never used for 403s

    Nginx-->>Build: 403 Forbidden
```

## Key Design Decisions

- **Cache key is the original request URI**, not the redirect target. S3 presigned URLs
  are ephemeral and unique per request, so keying on them would result in zero cache hits.

- **Upstream is always contacted.** AllowList locations never serve directly from cache,
  ensuring authorization and bans (403) are always enforced by Nexus.

- **Nginx follows redirects internally.** Clients see `200` with content, never `302`.
  Handled via `proxy_intercept_errors` and `error_page 301 302 307 308 = @handle_redirect`.

- **Dogpile protection.** `proxy_cache_lock on` ensures only one request fetches from S3
  when multiple concurrent requests arrive for the same uncached artifact.

- **Stale serving.** `proxy_cache_use_stale` serves cached content when S3 is temporarily
  unavailable.

## Cache Configuration

| Setting | Chart Default | Production | Description |
|---------|---------------|------------|-------------|
| `nginx.cache.ttl` | `1d` | `30d` | How long cached responses are considered fresh |
| `nginx.cache.size` | `1024` MiB | `1024` MiB | Maximum disk cache size |
| `inactive` | `7d` (hardcoded) | `7d` | Evict items not accessed within this period |
| `nginx.cache.allowList` | `[]` | configured | URL patterns routed through redirect caching |

> **Note:** Even with a 30-day TTL, items not accessed for 7 days are evicted due to the
> hardcoded `inactive=7d` setting in the nginx ConfigMap.
