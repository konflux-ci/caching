FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:28ec2f4662bdc4b0d4893ef0d8aebf36a5165dfb1d1dc9f46319bd8a03ed3365 AS squid-base

ENV NAME="konflux-ci/squid"
ENV SUMMARY="The Squid proxy caching server for Konflux CI"
ENV DESCRIPTION="\
    Squid is a high-performance proxy caching server for Web clients, \
    supporting FTP, gopher, and HTTP data objects. Unlike traditional \
    caching software, Squid handles all requests in a single, \
    non-blocking, I/O-driven process. Squid keeps metadata and especially \
    hot objects cached in RAM, caches DNS lookups, supports non-blocking \
    DNS lookups, and implements negative caching of failed requests."

ENV SQUID_VERSION="6.10-6.el10_1.1"

LABEL name="$NAME"
LABEL summary="$SUMMARY"
LABEL description="$DESCRIPTION"
LABEL usage="podman run -d --name squid -p 3128:3128 $NAME"
LABEL maintainer="bkorren@redhat.com"
LABEL com.redhat.component="konflux-ci-squid-container"
LABEL io.k8s.description="$DESCRIPTION"
LABEL io.k8s.display-name="konflux-ci-squid"
LABEL io.openshift.expose-services="3128:squid"
LABEL io.openshift.tags="squid"

# default port providing cache service
EXPOSE 3128

# default port for communication with cache peers
EXPOSE 3130

COPY LICENSE /licenses/

RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y "squid-${SQUID_VERSION}" && microdnf clean all

COPY --chmod=0755 container-entrypoint.sh /usr/sbin/container-entrypoint.sh

# Set up permissions for squid directories
RUN chown -R root:root /etc/squid/squid.conf /var/log/squid /var/spool/squid /run/squid && \
    chmod g=u /etc/squid/squid.conf /run/squid /var/spool/squid /var/log/squid

# ==========================================
# Stage 2: Combined Go builder (toolchain + exporters + helpers)
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:28ec2f4662bdc4b0d4893ef0d8aebf36a5165dfb1d1dc9f46319bd8a03ed3365 AS go-builder

# Install required packages for Go build
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y \
    tar \
    gzip \
    gcc \
    curl \
    ca-certificates \
    git && \
    microdnf clean all

# Install Go (version-locked)
ARG GO_VERSION=1.25.4
ARG GO_SHA256=9fa5ffeda4170de60f67f3aa0f824e426421ba724c21e133c1e35d6159ca1bec
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
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

# Set Go environment
ENV PATH="/usr/local/go/bin:/root/go/bin:$PATH"
ENV GOPATH="/root/go"
ENV GOCACHE="/tmp/go-cache"

WORKDIR /workspace

# Build both exporters in a single stage
# 1. Pre-fetch deps for exporters and helpers
COPY go.mod go.sum ./
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    go mod download

# 2. Build external squid-exporter (using prefetched modules)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    CGO_ENABLED=0 GOOS=linux go build -o /workspace/squid-exporter github.com/boynux/squid-exporter

# 3. Copy source and build the per-site exporter
COPY ./cmd/squid-per-site-exporter ./cmd/squid-per-site-exporter
RUN --mount=type=cache,target=/tmp/go-cache \
    if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    CGO_ENABLED=0 GOOS=linux go build -o /workspace/per-site-exporter ./cmd/squid-per-site-exporter

# 4. Copy source and build the store-id helper
COPY ./cmd/squid-store-id ./cmd/squid-store-id
RUN --mount=type=cache,target=/tmp/go-cache \
    if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    CGO_ENABLED=0 GOOS=linux go build -o /workspace/squid-store-id ./cmd/squid-store-id

COPY ./cmd/icap-server ./cmd/icap-server
RUN --mount=type=cache,target=/tmp/go-cache \
    if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    CGO_ENABLED=0 GOOS=linux go build -o /workspace/icap-server ./cmd/icap-server

# ==========================================
# Final Stage: Squid with integrated exporters and helpers
# ==========================================
FROM squid-base

# Copy all binaries from builder stage
COPY --from=go-builder \
    /workspace/squid-exporter \
    /workspace/per-site-exporter \
    /workspace/squid-store-id \
    /workspace/icap-server \
    /usr/local/bin/

# Set permissions for all binaries
RUN chmod +x \
    /usr/local/bin/squid-exporter \
    /usr/local/bin/per-site-exporter \
    /usr/local/bin/squid-store-id \
    /usr/local/bin/icap-server

# Expose exporters' metrics ports
EXPOSE 9301
EXPOSE 9302
# Expose ICAP port
EXPOSE 1344

USER 1001

ENTRYPOINT ["/usr/sbin/container-entrypoint.sh"]
