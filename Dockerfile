# Requires `promu crossbuild` artifacts.
ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as proxy
COPY pushprox-proxy /app/pushprox-proxy

ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as client
COPY pushprox-client /app/pushprox-client