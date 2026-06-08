---
name: running-tests
description: Gotchas when running unit or E2E tests
---

# Running Tests

- `mage all` and `mage test:cluster` fail differently: `helm test` failures appear in pod logs (`kubectl logs squid-test-...`), while `test:cluster` failures require a working mirrord binary and an active target pod -- check the latter first if tests hang
- After cluster tests, Helm release is **reset to defaults** automatically -- don't rely on test-modified state persisting
