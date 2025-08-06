# ==========================================
# Stage 1: Build per-site exporter
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:ce6e336ca4c1b153e84719f9a123b9b94118dd83194e10da18137d1c571017fe AS exporter-builder

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
# Stage 2: Final production image
# ==========================================
FROM registry.access.redhat.com/ubi10/ubi-minimal@sha256:ce6e336ca4c1b153e84719f9a123b9b94118dd83194e10da18137d1c571017fe

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

# Set permissions for the exporter binary
RUN chmod +x /usr/local/bin/squid-per-site-exporter

COPY LICENSE /licenses/

COPY --chmod=0755 container-entrypoint.sh /usr/sbin/container-entrypoint.sh

# Configure squid
RUN echo "pid_filename /run/squid/squid.pid" >> /etc/squid/squid.conf && \
    sed -i "s/# http_access allow localnet/http_access allow localnet/g" /etc/squid/squid.conf && \
    chown -R root:root /etc/squid/squid.conf /var/log/squid /var/spool/squid /run/squid && \
    chmod g=u /etc/squid/squid.conf /run/squid /var/spool/squid /var/log/squid

# Verify squid is installed
RUN /usr/sbin/squid -v

USER 1001

ENTRYPOINT ["/usr/sbin/container-entrypoint.sh"]
