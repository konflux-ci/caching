---
name: debugging-running-instance
description: Gotchas when debugging a deployed Squid instance
---

# Debugging Running Instances

- The chart deploys **both Squid and Nginx** -- use label selectors (`app.kubernetes.io/component=squid-caching` or `nginx-caching`) to find the right pods
- Cache auto-cleans when disk usage **exceeds 95%** on container start -- if cache is empty after restart, check `CACHE_USAGE_THRESHOLD` env var
- Unexpected pod restarts may be **certificate rotation**, not crashes -- the entrypoint watches `/etc/squid/certs/tls.crt` every 30s and exits when the cert changes
