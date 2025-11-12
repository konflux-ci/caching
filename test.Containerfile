FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:ebc9604c67aa5daa87cd431d64754a1cb6e22372446a6e8e0d966bfc709c9f3f

# Install required packages for Go and testing (version-locked)
# Note: curl-minimal is already present in ubi10-minimal
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y \
    tar-2:1.35-7.el10 \
    gzip-1.13-3.el10 \
    which-2.21-44.el10_0 \
    procps-ng-4.0.4-8.el10 \
    gcc-14.3.1-2.1.el10 \
    shadow-utils-2:4.15.0-8.el10 && \
    microdnf clean all

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Install Go (version-locked)
ARG GO_VERSION=1.25.3
ARG GO_SHA256=0335f314b6e7bfe08c3d0cfaa7c19db961b7b99fb20be62b0a826c992ad14e0f
# Use prefetched Go tarball from Cachi2
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    if [ -f /cachi2/output/deps/generic/go${GO_VERSION}.linux-amd64.tar.gz ]; then \
        cp /cachi2/output/deps/generic/go${GO_VERSION}.linux-amd64.tar.gz go.tar.gz; \
    else \
        curl -fsSL "https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o go.tar.gz; \
    fi && \
    echo "${GO_SHA256}  go.tar.gz" | sha256sum -c - && \
    tar -C /usr/local -xzf go.tar.gz && \
    rm go.tar.gz

# Install Helm (version-locked)
ARG HELM_VERSION=v3.18.6
ARG HELM_SHA256=3f43c0aa57243852dd542493a0f54f1396c0bc8ec7296bbb2c01e802010819ce
# Use prefetched Helm tarball from Cachi2
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    if [ -f /cachi2/output/deps/generic/helm-${HELM_VERSION}-linux-amd64.tar.gz ]; then \
        cp /cachi2/output/deps/generic/helm-${HELM_VERSION}-linux-amd64.tar.gz helm.tar.gz; \
    else \
        curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" -o helm.tar.gz; \
    fi && \
    echo "${HELM_SHA256}  helm.tar.gz" | sha256sum -c - && \
    mkdir -p /tmp/helm && \
    tar -C /tmp/helm -xzf helm.tar.gz && \
    mv /tmp/helm/linux-amd64/helm /usr/local/bin/helm && \
    rm -rf /tmp/helm helm.tar.gz

# Set Go environment
ENV PATH="/usr/local/go/bin:/root/go/bin:$PATH"
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

# Copy test source files maintaining directory structure
COPY tests/ ./tests/

# Copy squid chart
COPY squid/ ./squid/

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
LABEL maintainer="bkorren@redhat.com"
LABEL com.redhat.component="konflux-ci-squid-tester"
LABEL io.k8s.description="Konflux CI Squid Tester"
LABEL io.k8s.display-name="konflux-ci-squid-tester"
LABEL io.openshift.expose-services="3128:squid"
LABEL io.openshift.tags="squid-tester"

# Default command runs the compiled test binary
CMD ["./tests/e2e/e2e.test", "-ginkgo.v"] 
