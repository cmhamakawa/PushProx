# Requires `promu crossbuild` artifacts.
ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as proxy
COPY pushprox-proxy /app/pushprox-proxy
ENTRYPOINT ["/app/pushprox-proxy"]

ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:glibc as client
COPY pushprox-client /app/pushprox-client
ENTRYPOINT ["/app/pushprox-client", "--proxy-url=http://proxy:8080/", "--fqdn=core-dns.kube-system.svc"]

FROM prom/prometheus as prometheus
ADD prometheus.yml /etc/prometheus/

