# Requires `promu crossbuild` artifacts.
ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as proxy
COPY pushprox-proxy /app/pushprox-proxy

ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as client
COPY pushprox-client /app/pushprox-client

FROM prom/prometheus as prometheus
ADD prometheus.yml /etc/prometheus/
# The default startup is the proxy.
# This can be overridden with the docker --entrypoint flag or the command
# field in Kubernetes container v1 API.
