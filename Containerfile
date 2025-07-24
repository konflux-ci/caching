# ==========================================
# Stage 1: Build per-site exporter
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:5a57b4c2509df8df587e19cc7c2d9cfa45b012139f5decd77f942daeb2334228 AS exporter-builder

# Install Go and build dependencies
RUN microdnf install -y go ca-certificates && \
    microdnf clean all

# Set working directory for build
WORKDIR /workspace

# Copy go module files first for better layer caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code for the per-site exporter
COPY cmd/squid-per-site-exporter/ ./cmd/squid-per-site-exporter/

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o squid-per-site-exporter ./cmd/squid-per-site-exporter/

# ==========================================
# Stage 3: Final production image
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:ce6e336ca4c1b153e84719f9a123b9b94118dd83194e10da18137d1c571017fe AS squid-base

ENV NAME="konflux-ci/squid"
ENV SUMMARY="The Squid proxy caching server for Konflux CI"
ENV DESCRIPTION="\
    Squid is a high-performance proxy caching server for Web clients, \
    supporting FTP, gopher, and HTTP data objects. Unlike traditional \
    caching software, Squid handles all requests in a single, \
    non-blocking, I/O-driven process. Squid keeps metadata and especially \
    hot objects cached in RAM, caches DNS lookups, supports non-blocking \
    DNS lookups, and implements negative caching of failed requests."

ENV SQUID_VERSION="6.10-5.el10"

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

# port for per-site metrics
EXPOSE 9302

# Install only squid (no build tools in production image)
RUN microdnf install -y squid-${SQUID_VERSION} && \
    microdnf clean all

# Copy the built binary from the builder stage
COPY --from=exporter-builder /workspace/squid-per-site-exporter /usr/local/bin/squid-per-site-exporter

RUN chmod +x /usr/local/bin/squid-per-site-exporter

COPY LICENSE /licenses/

COPY --chmod=0755 container-entrypoint.sh /usr/sbin/container-entrypoint.sh

# Configure squid
RUN echo "pid_filename /run/squid/squid.pid" >> /etc/squid/squid.conf && \
    sed -i "s/# http_access allow localnet/http_access allow localnet/g" /etc/squid/squid.conf && \
    chown -R root:root /etc/squid/squid.conf /var/log/squid /var/spool/squid /run/squid && \
    chmod g=u /etc/squid/squid.conf /run/squid /var/spool/squid /var/log/squid && \
    chgrp -R squid /var/log/squid /var/spool/squid || true && \
    chmod -R g+wX /var/log/squid /var/spool/squid

# Provide default self-signed certs for Squid SSL-Bump so the pod starts before cert-manager provisions real certs
RUN microdnf install -y openssl && \
    mkdir -p /etc/squid/certs && \
    openssl req -x509 -nodes -newkey rsa:2048 \
      -keyout /etc/squid/certs/tls.key \
      -out /etc/squid/certs/tls.crt \
      -days 365 \
      -subj "/CN=localhost" && \
    chmod 0644 /etc/squid/certs/tls.crt && \
    chmod 0600 /etc/squid/certs/tls.key && \
    microdnf remove -y openssl && microdnf clean all || true

# ==========================================
# Stage 4: Build squid-exporter
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:5a57b4c2509df8df587e19cc7c2d9cfa45b012139f5decd77f942daeb2334228 AS squid-exporter-builder

# Install required packages for Go build
RUN microdnf install -y \
    tar \
    gzip \
    gcc \
    curl \
    ca-certificates \
    git && \
    microdnf clean all

# Install Go (version-locked)
ARG GO_VERSION=1.24.4
ARG GO_SHA256=77e5da33bb72aeaef1ba4418b6fe511bc4d041873cbf82e5aa6318740df98717
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl -fsSL "https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o go.tar.gz && \
    echo "${GO_SHA256}  go.tar.gz" | sha256sum -c - && \
    tar -C /usr/local -xzf go.tar.gz && \
    rm go.tar.gz

# Set Go environment
ENV PATH="/usr/local/go/bin:/root/go/bin:$PATH"
ENV GOPATH="/root/go"
ENV GOCACHE="/tmp/go-cache"

# Set working directory for build
WORKDIR /workspace

# Install squid-exporter directly from upstream and copy to expected location
RUN CGO_ENABLED=0 GOOS=linux go install github.com/boynux/squid-exporter@v1.13.0 && \
    cp /root/go/bin/squid-exporter /workspace/squid-exporter

# ==========================================
# Final Stage: Squid with integrated exporters
# ==========================================
FROM squid-base

# Copy squid-exporter binary from builder stage
COPY --from=squid-exporter-builder /workspace/squid-exporter /usr/local/bin/squid-exporter

# Set permissions for squid-exporter
RUN chmod +x /usr/local/bin/squid-exporter

# Expose squid-exporter metrics port (only in final stage where the binary exists)
EXPOSE 9301

USER 1001

ENTRYPOINT ["/usr/sbin/container-entrypoint.sh"]
