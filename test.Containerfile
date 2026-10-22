# Three large UBI bases with zero shared layers (~1195 MiB unique compressed)
# so Squid layer-cache A/B tests pull substantial, non-overlapping content.
# Warm stages are referenced via COPY --from so buildah --skip-unused-stages
# still pulls them.
FROM registry.access.redhat.com/ubi9/go-toolset:1.24@sha256:6234f572204d672a0ee0686d748fbb9b7b05679368bf0d7a4446e13970e58060 AS warm-ubi9-go
FROM registry.access.redhat.com/ubi9/python-312:latest@sha256:f43a57e9a6a2243e333008219002b9f2b1661868a94199f5dec4d602e1501482 AS warm-ubi9-python

FROM registry.access.redhat.com/ubi10/go-toolset:latest@sha256:321516119f1b2487e114ee09a8ae29cfa81c959449a0fc63c8c94e1a2dcf69f5

# Force warm stages to be materialized (otherwise skip-unused-stages drops them).
COPY --from=warm-ubi9-go /etc/os-release /tmp/warm-ubi9-go-os-release
COPY --from=warm-ubi9-python /etc/os-release /tmp/warm-ubi9-python-os-release

# go-toolset image runs as non-root by default; chown as root.
USER 0

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Set Go environment (GOPATH needed for go mod download)
# go-toolset installs to /usr/bin/go (already in PATH)
ENV PATH="/root/go/bin:$PATH"
ENV GOPATH="/root/go"
ENV GOCACHE="/tmp/go-cache"

# Create working directory
WORKDIR /app

# Copy module files first
COPY go.mod go.sum ./

# Download Go modules with Cachi2 environment
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    go mod download

# Install Ginkgo CLI (using prefetched modules)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    go build -o /usr/local/bin/ginkgo github.com/onsi/ginkgo/v2/ginkgo

# Install Helm CLI (using prefetched modules)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    go build -o /usr/local/bin/helm helm.sh/helm/v3/cmd/helm

# Copy test source files maintaining directory structure
COPY tests/ ./tests/

# Copy caching chart
COPY caching/ ./caching/

# Copy test entrypoint script
COPY --chmod=0755 test-entrypoint.sh ./test-entrypoint.sh

# Compile tests, mirrord target, and nginx test backend server
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    ginkgo build ./tests/e2e && \
    CGO_ENABLED=1 go build -o /app/mirrord-target ./tests/mirrord-target && \
    go build -o /app/nginx-test-backend ./tests/nginx-test-backend

# go-toolset already provides UID 1001 (default); reuse it for the final image.
RUN chown -R 1001:0 /app
USER 1001

LABEL name="Konflux CI Caching Tester"
LABEL summary="Konflux CI Caching Tester"
LABEL description="Konflux CI Caching Tester"
LABEL usage="podman run --rm konflux-ci/caching-tester"
LABEL maintainer="bkorren@redhat.com"
LABEL com.redhat.component="konflux-ci-caching-tester"
LABEL io.k8s.description="Konflux CI Caching Tester"
LABEL io.k8s.display-name="konflux-ci-caching-tester"
LABEL io.openshift.expose-services="3128:squid"
LABEL io.openshift.tags="caching-tester"
LABEL version="1.0"
LABEL release="1"
LABEL vendor="Red Hat, Inc."
LABEL distribution-scope="public"
LABEL url="https://github.com/konflux-ci/caching"

# Default command runs the compiled test binary
CMD ["./tests/e2e/e2e.test", "-ginkgo.v"]
