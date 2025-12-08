FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:a13cec4e2e30fa2ca6468d474d02eb349200dc4a831c8c93f05a2154c559f09b

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

# Copy squid chart
COPY squid/ ./squid/

# Copy test entrypoint script
COPY --chmod=0755 test-entrypoint.sh ./test-entrypoint.sh

# Compile tests and testserver at build time
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    ginkgo build ./tests/e2e && \
    CGO_ENABLED=1 go build -o /app/testserver ./tests/testserver

# Create a non-root user for running tests
RUN adduser --uid 1001 --gid 0 --shell /bin/bash --create-home testuser
USER 1001

LABEL name="Konflux CI Squid Tester"
LABEL summary="Konflux CI Squid Tester"
LABEL description="Konflux CI Squid Tester"
LABEL usage="podman run --rm konflux-ci/squid-tester"
LABEL maintainer="bkorren@redhat.com"
LABEL com.redhat.component="konflux-ci-squid-tester"
LABEL io.k8s.description="Konflux CI Squid Tester"
LABEL io.k8s.display-name="konflux-ci-squid-tester"
LABEL io.openshift.expose-services="3128:squid"
LABEL io.openshift.tags="squid-tester"
LABEL version="1.0"
LABEL release="1"
LABEL vendor="Red Hat, Inc."
LABEL distribution-scope="public"
LABEL url="https://github.com/konflux-ci/caching"

# Default command runs the compiled test binary
CMD ["./tests/e2e/e2e.test", "-ginkgo.v"]
