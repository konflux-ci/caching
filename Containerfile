FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:7bd3d2e7f5c507aebd1575d0f2fab9fe3e882e25fee54fa07f7970aa8bbc5fab AS squid-base

# default port providing cache service
EXPOSE 3128

# default port for communication with cache peers
EXPOSE 3130

COPY LICENSE /licenses/

RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y squid && microdnf clean all

COPY --chmod=0755 container-entrypoint.sh /usr/sbin/container-entrypoint.sh

# Set up permissions for squid directories
RUN chown -R root:root /etc/squid/squid.conf /var/log/squid /var/spool/squid /run/squid && \
    chmod g=u /etc/squid/squid.conf /run/squid /var/spool/squid /var/log/squid

# ==========================================
# Stage 2: Combined Go builder (toolchain + exporters + helpers)
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:7bd3d2e7f5c507aebd1575d0f2fab9fe3e882e25fee54fa07f7970aa8bbc5fab AS go-builder

# Install required packages for Go build
# go-toolset already declared in rpms.in.yaml (prefetched by Cachi2)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    microdnf install -y \
    tar \
    gzip \
    gcc \
    curl \
    ca-certificates \
    git \
    go-toolset && \
    microdnf clean all

# Set Go environment (GOPATH needed for go mod download)
# go-toolset installs to /usr/bin/go (already in PATH)
ENV PATH="/root/go/bin:$PATH"
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

# 2b. Build external access-log-exporter (using prefetched modules)
RUN if [ -f /cachi2/cachi2.env ]; then . /cachi2/cachi2.env; fi && \
    CGO_ENABLED=0 GOOS=linux go build -o /workspace/access-log-exporter github.com/jkroepke/access-log-exporter/cmd/access-log-exporter

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
# Stage: access-log-exporter only (for use as sidecar with nginx monitoring).
# Build with: podman build --target access-log-exporter -t access-log-exporter .
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:7bd3d2e7f5c507aebd1575d0f2fab9fe3e882e25fee54fa07f7970aa8bbc5fab AS access-log-exporter

ENV NAME="konflux-ci/access-log-exporter"
ENV SUMMARY="Access-log-exporter for Prometheus metrics from NGINX access logs (Konflux CI)"
ENV DESCRIPTION="\
    Minimal image containing only the access-log-exporter binary for use as a \
    sidecar with NGINX. Exposes Prometheus metrics from NGINX access logs."

LABEL name="$NAME"
LABEL summary="$SUMMARY"
LABEL description="$DESCRIPTION"
LABEL usage="podman run -d --name access-log-exporter $NAME"
LABEL maintainer="hmariset@redhat.com"
LABEL com.redhat.component="konflux-ci-access-log-exporter-container"
LABEL io.k8s.description="$DESCRIPTION"
LABEL io.k8s.display-name="konflux-ci-access-log-exporter"
LABEL io.openshift.tags="exporter,metrics"
LABEL version="1.0"
LABEL release="1"
LABEL vendor="Red Hat, Inc."
LABEL distribution-scope="public"
LABEL url="https://github.com/konflux-ci/caching"

# Copy access-log-exporter binary from builder stage
COPY --from=go-builder /workspace/access-log-exporter /usr/local/bin/

# Set permissions
RUN chmod +x /usr/local/bin/access-log-exporter

USER 1001

ENTRYPOINT ["/usr/local/bin/access-log-exporter"]

# ==========================================
# Final stage (default build target): Squid with integrated exporters and helpers.
# CI builds this image when building Containerfile without --target.
# ==========================================
FROM squid-base AS squid

ENV NAME="konflux-ci/squid"
ENV SUMMARY="The Squid proxy caching server for Konflux CI"
ENV DESCRIPTION="\
    Squid is a high-performance proxy caching server for Web clients, \
    supporting FTP, gopher, and HTTP data objects. Unlike traditional \
    caching software, Squid handles all requests in a single, \
    non-blocking, I/O-driven process. Squid keeps metadata and especially \
    hot objects cached in RAM, caches DNS lookups, supports non-blocking \
    DNS lookups, and implements negative caching of failed requests."

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
LABEL version="1.0"
LABEL release="1"
LABEL vendor="Red Hat, Inc."
LABEL distribution-scope="public"
LABEL url="https://github.com/konflux-ci/caching"

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
