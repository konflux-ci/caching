# Root Cause Analysis: The 3 Failing Tests

## The Core Issue

**ConfigureSquidWithHelm() ALREADY sets `mirrord.enabled=false` in prerelease/EaaS!**

```go
// From proxy_test_helpers.go lines 473-482
if environment == "prerelease" {
    extraArgs = []string{
        "--set", "installCertManagerComponents=false",
        "--set", "cert-manager.enabled=false",
        "--set", "trust-manager.enabled=false",
        "--set", "mirrord.enabled=false",  // ✅ ALREADY DISABLING IT!
    }
}
```

So the question is: **Why does Helm still try to "replace" the mirrord-test-target pod?**

## Hypothesis 1: Helm Release State Confusion

**The Scenario:**
1. EaaS pipeline: `helm install squid ... --set mirrord.enabled=false` → No mirrord pod created
2. Test runs: `helm upgrade squid ... --set mirrord.enabled=false` → Should be no-op
3. But Helm sees the template file exists and tries to reconcile it
4. Even though the conditional `{{- if .Values.mirrord.enabled }}` is false, Helm might still process the resource metadata
5. Helm tries to "replace" a pod that doesn't exist → Permission error

## Hypothesis 2: Namespace Mismatch

**The Scenario:**
1. Pipeline installs: `helm install squid -n=default` 
2. Test upgrades: `helm upgrade squid -n=default`
3. But the test pod's ServiceAccount is in the `caching` namespace
4. When Helm tries to apply resources in `caching` namespace, it uses the test pod's ServiceAccount
5. That ServiceAccount doesn't have permissions to create pods with mirrord's security context

## Hypothesis 3: Test Pod ServiceAccount Restrictions

**The Problem:**
The test pod runs with ServiceAccount `squid-test`, which has limited RBAC permissions.

Looking at `squid/templates/test-rbac.yaml`, the ServiceAccount might not have:
- `pods/create` permission
- Permission to use specific security context constraints

When `helm upgrade` tries to reconcile resources, it runs as the test pod's identity, which lacks necessary permissions.

## The Real Solution

Looking at the code flow:

```
ConfigureSquidWithHelm()
  ↓
  Check SKIP_HELM_RECONFIGURE  ← MY NEW FIX (returns early if true)
  ↓
  Set environment from SQUID_ENVIRONMENT
  ↓
  Build values struct
  ↓
  If environment=="prerelease": add extraArgs with mirrord.enabled=false
  ↓
  Call UpgradeChartWithArgs()
  ↓
  Run: helm upgrade --install squid ./squid -n=default --values /tmp/values-xxx.yaml --set mirrord.enabled=false
  ↓
  FAILS with "pods mirrord-test-target is forbidden"
```

**The fix I just pushed (SKIP_HELM_RECONFIGURE) prevents this entire flow from running in EaaS.**

## But Will Tests Pass?

**NO!** Because tests need specific configurations:

### Test 1: SSL-Bump
**Needs:** `tlsOutgoingOptions.caFile: "/etc/squid/trust/test-server/ca.crt"`  
**Gets:** `tlsOutgoingOptions.caFile: ""` (default)  
**Result:** ❌ **WILL FAIL** - Can't decrypt HTTPS without CA file

### Test 2: Cache Allow List  
**Needs:** `cache.allowList: ["^http://.*/do-cache.*"]`  
**Gets:** `cache.allowList: []` (empty = cache everything)  
**Result:** ❓ **MIGHT PASS** - Depends on test logic:
- First test: "should cache all requests by default" → ✅ Works with default config
- Second test: "should cache HTTP requests that match allowList patterns" → ❌ Fails (needs specific allowList)
- Third test: "should NOT cache requests that don't match allowList patterns" → ❌ Fails (needs allowList)

### Test 3: Container Image Pulls
**Needs:** `cache.allowList: [quay CDN patterns]`  
**Gets:** `cache.allowList: []` (empty = cache everything)  
**Result:** ✅ **MIGHT PASS** - Empty allowList means "cache everything", which includes quay CDN URLs

## The REAL Real Solution

We have 3 options:

### Option A: Pre-configure EaaS Pipeline (RECOMMENDED)
Deploy Squid in EaaS with ALL test configurations:

```yaml
# In .tekton/squid-e2e-eaas-test.yaml
helm install squid . \
  --set tlsOutgoingOptions.caFile="/etc/squid/trust/test-server/ca.crt" \
  --set cache.allowList[0]="^http://.*/do-cache.*" \
  --set cache.allowList[1]="^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}" \
  ...
```

**Problem:** These settings conflict:
- SSL-Bump needs CA file set
- Cache Allow List test needs to test BOTH empty allowList AND specific patterns
- Can't have both simultaneously

### Option B: Skip These Tests in EaaS
Add skip logic:
```go
BeforeAll(func() {
    if os.Getenv("SKIP_HELM_RECONFIGURE") == "true" {
        Skip("Test requires dynamic reconfiguration, not supported in EaaS")
    }
    ...
})
```

### Option C: Grant Test Pod More Permissions
Modify test-rbac.yaml to allow the test ServiceAccount to run helm upgrade:
- Add `pods/create`, `pods/update`, `pods/delete` permissions
- Add permission to use various SecurityContextConstraints

**Problem:** Security risk, might not be allowed in EaaS

### Option D: Split Tests into Static and Dynamic
- "Static" tests: Work with default config, run in EaaS
- "Dynamic" tests: Need reconfiguration, only run in devcontainer

## Recommended Action

**Let's wait for current test results**, then:

1. If Container Image Pulls **PASSES** → Great! 1/3 tests fixed
2. If SSL-Bump **FAILS** (likely) → Consider Option B (skip in EaaS)
3. If Cache Allow List **PARTIALLY PASSES** → Split test into static vs dynamic scenarios

**The fundamental issue:** These tests were designed for local devcontainer development where `helm upgrade` works. EaaS has stricter security constraints that prevent dynamic Helm reconfiguration from within test pods.

