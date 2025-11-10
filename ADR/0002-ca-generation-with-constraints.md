# 2. CA generation with security constraints

Date: 2025-11-09

## Status

Accepted

## Context

The Squid proxy uses SSL bumping to intercept HTTPS traffic, requiring a Certificate
Authority (CA) to sign dynamically generated certificates. The primary security risk is
CA private key compromise, which would allow an attacker to sign certificates for any
domain.

To mitigate this risk, we need to apply X.509 certificate constraints:
- **pathLenConstraint=0**: Prevents creation of intermediate CAs, forcing direct signing
  by the root CA
- **Name Constraints**: Limits which domains the CA can issue certificates for
- **Key Usage**: Restricts certificate to CA operations only (cert sign, CRL sign)

cert-manager cannot generate CAs with `pathLenConstraint=0`.

## Decision

We will generate the root CA certificate externally using OpenSSL (or a similar tool)
with the required constraints, then distribute it via Kubernetes Secrets and
trust-manager.

**CA Specifications:**
- Generate CA with OpenSSL (or a similar tool) using a configuration file specifying:
  - `pathLenConstraint=0` (Basic Constraints)
  - Name Constraints (permitted: `registry.access.redhat.com`, `registry.redhat.io`,
    `*.redhat.com`, `*.access.redhat.com`, `*.connect.redhat.com`, `quay.io`,
    `.quay.io`, `cdn.quay.io`, `cdn01.quay.io`, `cdn02.quay.io`; excluded: `*`)
  - Key Usage (`keyCertSign`, `cRLSign` only)
  - TBD validity period

**Note:** `*.redhat.com`, `*.access.redhat.com`, and `*.connect.redhat.com` are required
because Squid copies DNS names from upstream certificates when generating certificates.
The `registry.access.redhat.com` certificate includes `*.redhat.com` in its Subject
Alternative Name (SAN), which must be permitted by Name Constraints. Additional hosts
will be required within the list of permitted hosts. E.g. additional cdnXX.quay.io
hosts.

**Example OpenSSL commands:**

```bash
# 1. Generate CA private key
openssl genrsa -out ca-key.pem 2048

# 2. Create OpenSSL config file (ca.conf) with constraints
cat > ca.conf << 'EOF'
[req]
distinguished_name = req_distinguished_name
[req_distinguished_name]

[v3_ca]
# Basic Constraints with pathLenConstraint=0
basicConstraints = critical,CA:TRUE,pathlen:0

# Key Usage - restricted to CA operations only
keyUsage = critical,keyCertSign,cRLSign

# Name Constraints
nameConstraints = critical,@name_constraints

[name_constraints]
# Permitted domains
permitted;DNS.1 = registry.access.redhat.com
permitted;DNS.2 = registry.redhat.io
permitted;DNS.3 = *.redhat.com
permitted;DNS.4 = *.access.redhat.com
permitted;DNS.5 = *.connect.redhat.com
permitted;DNS.6 = quay.io
permitted;DNS.7 = .quay.io
permitted;DNS.8 = cdn.quay.io
permitted;DNS.9 = cdn01.quay.io
permitted;DNS.10 = cdn02.quay.io

# Excluded domains
excluded;DNS.1 = *
EOF

# 3. Generate certificate signing request
openssl req -new -key ca-key.pem -out ca.csr \
    -subj "/CN=constrained-ca/O=konflux" \
    -config ca.conf

# 4. Self-sign CA certificate with constraints
openssl x509 -req -in ca.csr -signkey ca-key.pem \
    -out ca.crt -days 90 \
    -extensions v3_ca -extfile ca.conf
```

**CA Distribution:**
- Store CA certificate and private key in `caching` namespace (for Squid)
- Store CA certificate only in `cert-manager` namespace (for trust-manager)
- Use trust-manager Bundle to distribute CA to client namespaces
- In production/staging: Generate CA externally (Vault), sync via External Secrets Operator

**Example trust-manager Bundle manifest:**

```yaml
# trust-manager-bundle.yaml
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: caching-ca-bundle
spec:
  sources:
  - secret:
      name: caching-root-ca-secret
      key: ca.crt
  target:
    configMap:
      key: ca-bundle.crt
    namespaceSelector: {}
```

**Deploying CA to cluster:**

After generating the CA with OpenSSL, create Kubernetes Secrets:

```bash
# Create secret in caching namespace (required by deployment template)
kubectl create secret tls caching-tls \
    --cert=ca.crt \
    --key=ca-key.pem \
    --namespace=caching \
    --dry-run=client -o yaml | kubectl apply -f -

# Create secret in cert-manager namespace (cert only for trust-manager)
kubectl create secret generic caching-root-ca-secret \
    --from-file=ca.crt=ca.crt \
    --namespace=cert-manager \
    --dry-run=client -o yaml | kubectl apply -f -

# Apply trust-manager Bundle
kubectl apply -f trust-manager-bundle.yaml
```

**Dev vs Production:**
- Dev: Generate CA with OpenSSL script, deploy manually (matches prod process for
  testing)
- Production/Staging: Generate CA externally, sync via External Secrets Operator

## Consequences

Positive:
- `pathLenConstraint=0` prevents intermediate CA creation even if root key is compromised
- Name Constraints limit domain scope of compromised CA
- Key Usage restricts certificate to CA operations only

Negative:
- Manual CA generation process (no cert-manager lifecycle management)
- CA rotation requires manual regeneration and secret updates
- Additional operational overhead for CA management
