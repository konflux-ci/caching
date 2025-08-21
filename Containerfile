FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:d8cba62fbd44610595a6ce7badd287ca4c9985cbe9df55cc9b6a5c311b9a46e6 AS squid-base

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

COPY LICENSE /licenses/

RUN microdnf install -y "squid-${SQUID_VERSION}" && microdnf clean all

COPY --chmod=0755 container-entrypoint.sh /usr/sbin/container-entrypoint.sh

# move location of pid file to a directory where squid user can recreate it
RUN echo "pid_filename /run/squid/squid.pid" >> /etc/squid/squid.conf && \
    sed -i "s/# http_access allow localnet/http_access allow localnet/g" /etc/squid/squid.conf && \
    chown -R root:root /etc/squid/squid.conf /var/log/squid /var/spool/squid /run/squid && \
    chmod g=u /etc/squid/squid.conf /run/squid /var/spool/squid /var/log/squid

# ==========================================
# Stage 2: Build squid-exporter
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:d8cba62fbd44610595a6ce7badd287ca4c9985cbe9df55cc9b6a5c311b9a46e6 AS squid-exporter-builder

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
# Final Stage: Squid with integrated exporter
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
