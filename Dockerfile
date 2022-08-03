# Requires `promu crossbuild` artifacts.
ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as proxy
COPY pushprox-proxy /app/pushprox-proxy
USER	nobody
ENTRYPOINT ["/app/pushprox-proxy"]

ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as client
COPY pushprox-client /app/pushprox-client
USER	nobody
ENTRYPOINT ["/app/pushprox-client", "--proxy-url=http://proxy:8080/", "--fqdn=kube-dns.kube-system.svc"]

FROM prom/prometheus as prometheus
ADD prometheus.yaml /etc/prometheus/
# EXPOSE 9090

# ARG ARCH="amd64"
# ARG OS="linux"
# FROM golang:1.18 as goproxy
# COPY goproxy /app/goproxy
# USER	nobody
# ENTRYPOINT ["/app/goproxy", "--insecure=true"]
# The default startup is the proxy.
# This can be overridden with the docker --entrypoint flag or the command
# field in Kubernetes container v1 API.

