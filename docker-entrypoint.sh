#!/bin/sh
set -e

echo "Generating envoy.yaml config file..."
cat /tmpl/client_envoy.yaml.tmpl | envsubst \$API_SERVER_IP > /etc/client_envoy.yaml

echo "Starting Envoy..."
/usr/local/bin/envoy -c /etc/client_envoy.yaml
