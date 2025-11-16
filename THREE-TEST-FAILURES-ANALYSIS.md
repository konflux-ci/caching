# Analysis: Why 3 Tests Fail in EaaS

## Summary
**SSL-Bump**, **Cache Allow List**, and **Container Image Pulls** tests are the ONLY tests that fail in EaaS. All other tests pass.

## Key Finding: What Makes These Tests Different

These 3 tests are **the ONLY tests** that call `ConfigureSquidWithHelm()` to reconfigure Squid **OUTSIDE of BeforeSuite**.

```go
// ALL OTHER TESTS: No ConfigureSquidWithHelm calls

// ❌ FAILING TEST 1: SSL-Bump
BeforeAll(func() {
    err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
        TLSOutgoingOptions: &testhelpers.TLSOutgoingOptionsValues{
            CAFile: "/etc/squid/trust/test-server/ca.crt",
        },
        ReplicaCount: int(suiteReplicaCount),
    })
})

// ❌ FAILING TEST 2: Cache Allow List  
BeforeAll(func() {
    err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
        Cache: &testhelpers.CacheValues{
            AllowList: []string{"^http://.*/do-cache.*"},
        },
        ReplicaCount: int(suiteReplicaCount),
    })
})

// ❌ FAILING TEST 3: Container Image Pulls
func pullAndVerifyQuayCDN(imageRef string) {
    err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
        Cache: &testhelpers.CacheValues{
            AllowList: []string{
                "^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}",
                "dummy-" + imageRef,
            },
        },
    })
}
```

## The Original Error

From user's logs:
```
failed to upgrade squid with helm: failed to run helm upgrade command: exit status 1
Error: UPGRADE FAILED: failed to replace object: pods "mirrord-test-target" is forbidden: 
unable to validate against any security context constraint: [provider "anyuid": Forbidden: 
not usable by user or serviceaccount, provider "restricted": Forbidden: not usable by user 
or serviceaccount, ...]
```

## Why The Error Happens

1. **EaaS pipeline installs Squid** with `--set mirrord.enabled=false`
2. **Test pod tries to run `helm upgrade`** to reconfigure Squid
3. **Helm upgrade tries to reconcile the chart**, which includes the mirrord-test-target pod template
4. **Even though mirrord.enabled=false**, Helm still processes the template and tries to "replace" it
5. **The test pod's ServiceAccount lacks permissions** to create pods with those security contexts
6. **Helm upgrade fails** → Test fails

## Additional Issue: "2 squid pods found"

Some test runs showed:
```
Failed to get squid pod
Unexpected error: 2 squid pods found
```

This suggests duplicate pods were created during helm upgrades.

## Why SKIP_HELM_RECONFIGURE Partially Helps

The fix I just pushed makes `ConfigureSquidWithHelm()` check `SKIP_HELM_RECONFIGURE=true` and skip the helm upgrade entirely.

**This will:**
- ✅ Stop the "mirrord-test-target forbidden" errors
- ✅ Stop the "2 squid pods found" errors  
- ❌ BUT: Tests will use default EaaS config, not their specific configs

## The Real Problem

These tests **REQUIRE specific configurations** that differ from the default:

| Test | Required Config | Default EaaS Config | Will Test Pass? |
|------|----------------|---------------------|-----------------|
| **SSL-Bump** | `tlsOutgoingOptions.caFile: "/etc/squid/trust/test-server/ca.crt"` | `caFile: ""` | ❌ NO |
| **Cache Allow List** | `cache.allowList: ["^http://.*/do-cache.*"]` | `allowList: []` (cache all) | ❓ MAYBE |
| **Container Image Pulls** | `cache.allowList: [quay CDN patterns]` | `allowList: []` (cache all) | ✅ YES |

## Possible Solutions

### Option 1: Skip These Tests in EaaS
Add `Skip()` when `SKIP_HELM_RECONFIGURE=true`:
```go
BeforeAll(func() {
    if os.Getenv("SKIP_HELM_RECONFIGURE") == "true" {
        Skip("This test requires helm reconfiguration, skipping in EaaS")
    }
    err := testhelpers.ConfigureSquidWithHelm(...)
})
```

### Option 2: Configure EaaS Pipeline to Match Test Needs
Pre-configure Squid in the EaaS pipeline with all the settings these tests need:
- Set `tlsOutgoingOptions.caFile`
- Set `cache.allowList` to patterns that work for all tests

**Problem:** These settings might conflict (SSL-Bump needs CA file, Cache tests need empty allowList by default)

### Option 3: Fix Helm Upgrade Permissions
Grant the test pod's ServiceAccount permission to create/update pods with various security contexts.

**Problem:** Security risk, might not be allowed in EaaS environment

### Option 4: Use kubectl Instead of Helm
Modify `ConfigureSquidWithHelm()` to use `kubectl patch` or `kubectl set` commands instead of helm upgrade.

**Problem:** Loses Helm's state management and rollback capabilities

### Option 5: Make Tests Environment-Aware
Tests detect EaaS and adjust their expectations:
- SSL-Bump: Skip if no CA file configured
- Cache Allow List: Accept default behavior if can't reconfigure
- Container Image Pulls: Works with default config (allowList=[] caches everything)

## Recommended Next Steps

1. **Wait for test results** from current push to confirm SKIP_HELM_RECONFIGURE prevents helm errors
2. **Review actual test failures** to see if any tests pass with default config
3. **Decide on strategy:**
   - If Container Image Pulls passes → It doesn't need special config
   - If Cache Allow List fails → We need to understand why
   - SSL-Bump likely needs special handling (always requires CA file)

## Key Question

**Do these tests REALLY need dynamic reconfiguration, or can we pre-configure EaaS to support all test scenarios?**

