# 1. Caching container image layers from CDNs

Date: 2025-09-30

## Status

Accepted

## Context

Konflux and its pipelines pull container images from multiple registries. It's important to support caching
of these container image layers.

Commonly used registries include:
- quay.io and Red Hat registries (registry.access.redhat.com, registry.redhat.io)
- Docker Hub (docker.io)

These images may be public or private.

### Common Registry Behavior

Container registries typically redirect blob requests to CDN backends for efficient content delivery. 
These CDNs use content-addressable URLs containing SHA256 hashes (e.g., `/sha256/[hash]`) and include:
- Temporary credentials in query parameters
- Short-lived signatures to prevent abuse
- `Authorization` headers (even for public images in some cases)

The caching solution is registry-agnostic and works with any CDN that uses content-addressable URLs. Users can extend support to additional registries by adding their CDN URL patterns to the configuration.

### Example: Quay.io

When pulling a blob directly from quay.io, the registry returns a `302` redirect. The redirect target varies by region and load:

1. **CDN URLs** (`cdnNN.quay.io`): Used in most regions
2. **Direct S3 URLs**: Used in `us-east-1` region for direct blob storage access

Each CDN request has the following structure:

```
https://cdn01.quay.io/quayio-production-s3/sha256/0e/0ea0faeb23c26511e776010a311460a8e57c969b6d3f16484fa1dd29c22a613d?...
```

In `us-east-1`, Quay may redirect directly to S3. AWS S3 supports two URL formats, and Quay may use either depending on region and configuration:

**Path-style URLs** (bucket name in path):
```
https://s3.us-east-1.amazonaws.com/quayio-production-s3/sha256/55/551849...
https://s3.dualstack.us-east-1.amazonaws.com/quayio-production-s3/sha256/55/551849...
```

**Virtual-hosted-style URLs** (bucket name in hostname):
```
https://quayio-production-s3.s3.us-east-1.amazonaws.com/sha256/55/551849...
https://quayio-production-s3.s3.amazonaws.com/sha256/55/551849...
```

Both URL formats point to the same S3 bucket and must be supported to ensure consistent caching regardless of which format Quay returns.

See the [OpenShift Container Platform firewall configuration documentation]
(https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/installation_configuration/configuring-firewall#configuring-firewall_configuring-firewall)
which documents the `quayio-production-s3` S3 bucket as required to access Quay image content in AWS.

There's an accompanying `Authorization` header with each request, even for public images.

Additionally, these 302 redirects from quay.io include temporary credentials embedded in the query params.
There's also a signature which incorporates the host header in the request. A few of the noteworthy headers:

```
X-Amz-Algorithm=AWS4-HMAC-SHA256
X-Amz-Credential=AKIA...
X-Amz-Expires=600
X-Amz-SignedHeaders=host
X-Amz-Signature=954084aa3459195cbe82d4712a0c22c01e148df993eeeafdb3dcb3b736312375
```

When pulling a public image from registry.access.redhat.com there are no `Authorization` headers present.

### Challenges

1. Squid, by default, includes query params when calculating a cache key (a.k.a. store ID) for a request.
Since these params are short lived, we must tell squid to ignore them while computing this ID.
However, it's still critical to ensure subsequent client requests are authorized before serving from
the cache. There is a high risk of leaking private content to unauthorized users.

2. Squid, by design, does not cache responses to requests with `Authorization` unless very specific
conditions are met. See the Squid FAQ on caching with Authorization headers:
[Squid Internals: Why some objects do not get cached](https://wiki.squid-cache.org/SquidFaq/InnerWorkings#how-come-some-objects-do-not-get-cached).
We need some way to manipulate requests or responses in a way which impacts caching decisions but
does not tamper with the data passed between the client and the CDN.

### Options Considered

#### URL rewriting vs Store-ID

- `url_rewrite_program` (transparent rewrite, `url-rewrite=`): cannot modify the `Host` header on HTTPS
  requests. CDN signatures include `X-Amz-SignedHeaders=host` (and Akamai signatures), so any effective
  rewrite that would imply a host change invalidates the signature. Result: 403 from CDN.
- `url_rewrite_program` (standard redirects, `url=`): would require generating valid CDN-signed URLs,
  which is not possible without private keys.
- `store_id_program`: compute a stable cache key (store-id) for CDN URLs by stripping query parameters;
  also perform an authorization probe. CDN redirect URLs do not support `HEAD`, so the probe must use
  `GET` without reading the body.

#### Content Adaptation

Content Adaptation options (per [Squid Content Adaptation](https://wiki.squid-cache.org/SquidFaq/ContentAdaptation)):
- ICAP REQMOD: header-only adaptation at request time, can run at `reqmod_precache` before cacheability decisions.
- ICAP RESPMOD: would force blob payloads through ICAP; unnecessary for header-only changes.
- eCAP: in-process C++ modules; high complexity for this project.
- squid.conf ACL tricks: insufficient to handle `Authorization` cache rules safely.
- Client Streams/code hacks: limited support or too invasive.

## Decision

We'll create a custom store ID helper program to compute IDs for content-addressable URLs from:
1. Quay CDN: `^https://cdn(\d{2})?\.quay\.io/.+/sha256/.+/[a-f0-9]{64}`
2. Quay S3 backend (path-style): `^https://s3\.[a-z0-9-]+\.amazonaws\.com/quayio-production-s3/sha256/.+/[a-f0-9]{64}`
3. Quay S3 backend (virtual-hosted): `^https://quayio-production-s3\.s3[a-z0-9.-]*\.amazonaws\.com/sha256/.+/[a-f0-9]{64}`
4. Docker Hub CDN (Cloudflare R2): `^https://docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`
5. Docker Hub CDN (Cloudflare): `^https://production\.cloudflare\.docker\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`
6. Docker Hub S3 backend: `^https://docker-images-prod\.s3[a-z0-9.-]*\.amazonaws\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`

It will perform a lightweight GET authorization check to the original URL.
Given a `200 OK` response, it will return the same URL without query parameters to use as the stable
cache key. For any other response, it returns the original URL to avoid cache pollution.

Additionally, we'll create an ICAP server sidecar to drop the `Authorization` headers on requests
destined for the CDN. These altered requests will only be used for cache hit detection internally to
squid and will not be stripped from the outgoing CDN requests. Response modification (e.g. adding
the `Cache-Control: public` header) is to be avoided since it requires passing blob data through the
ICAP server.

**Pattern Configuration:** CDN URL patterns are configured via the `cache.allowList` Helm value, which is used by:
- Squid ACLs for caching decisions (`cache allow/deny`)
- Store-ID helper access control (`store_id_access`)
- ICAP server access control (`adaptation_access`)

This centralized approach ensures consistency and simplifies configuration management.

The resulting flow for pulling (and re-pulling) from the CDN would then look like:

```mermaid
sequenceDiagram
    participant Client as Client (buildah)
    participant Squid as Squid Proxy
    participant StoreID as Store-ID Helper
    participant ICAP as ICAP REQMOD
    participant Registry as quay.io
    participant CDN as cdn01.quay.io

    Note over Client,CDN: After client auth, first pull (misses on CDN, cache populated)

    Client->>Squid: GET /v2/.../blobs/sha256:0ea0...
    Squid->>Registry: GET blob
    Registry-->>Squid: 302 Location: https://cdn01.quay.io/...?
    Squid-->>Client: 302

    Client->>Squid: GET https://cdn01.quay.io/.../sha256/0e/0ea0...? (with Authorization)
    Squid->>ICAP: REQMOD (precache)
    ICAP-->>Squid: 200 (Authorization removed)
    Squid->>StoreID: request https://cdn01.quay.io/...0ea0...?
    StoreID->>CDN: GET original URL (auth check)
    CDN-->>StoreID: 200 (authorized)
    StoreID-->>Squid: OK store-id=https://cdn01.quay.io/.../sha256/0e/0ea0...
    Squid->>CDN: GET /...0ea0...? (original URL)
    CDN-->>Squid: 200 (octet-stream)
    Squid-->>Client: 200 (TCP_MISS, cached under store-id)

    Note over Client,Registry: Second pull (hits from cache on CDN URLs)

    Client->>Squid: GET /v2/... (auth + manifests as above)
    Squid->>Registry: GETs
    Registry-->>Squid: 200s
    Squid-->>Client: 200s

    Client->>Squid: GET https://cdn01.quay.io/.../sha256/0e/0ea0...? (with Authorization)
    Squid->>ICAP: REQMOD (precache)
    ICAP-->>Squid: 200 (Authorization removed)
    Squid->>StoreID: request https://cdn01.quay.io/...0ea0...?
    StoreID-->>Squid: OK store-id=https://cdn01.quay.io/.../sha256/0e/0ea0...
    Squid-->>Client: 200 (TCP_MEM_HIT, served from cache)
```

## Consequences

Positive:
- Stable, shareable cache for content-addressable layers across clients with differing credentials.
- Minimal adaptation: The client and server never see these changes.
- Blob payloads never traverse the ICAP server.
- Applies equally to Red Hat registries (`registry.access.redhat.com`, `registry.redhat.io`) which proxy to quay.io.
- Strict compliance with Squid's caching specifications for `Authorization`.
- Extensible to additional CDN providers (e.g., Docker Hub) without architectural changes.
- Centralized pattern configuration via `cache.allowList` ensures consistency across Squid ACLs and helpers.

Negative:
- Additional components to maintain (store ID helper and ICAP sidecar).
- Authorization checks in the store ID helper add an additional `GET` request per blob.
