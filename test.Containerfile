FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:76c113359a458e3f04057762b5bd4a9837a6987520434dea158c728280116713

# Install required packages for Go and testing
# Note: curl-minimal is already present in ubi10-minimal
# Using generic package names - exact versions are controlled by rpms.lock.yaml
# go-toolset already declared in rpms.in.yaml (prefetched by Cachi2)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y \
    tar \
    gzip \
    which \
    procps-ng \
    gcc \
    shadow-utils \
    go-toolset && \
    microdnf clean all

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

# Create a non-root user for running tests
RUN adduser --uid 1001 --gid 0 --shell /bin/bash --create-home testuser
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
