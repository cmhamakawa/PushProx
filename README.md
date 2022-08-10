# PushProx [![CircleCI](https://circleci.com/gh/prometheus-community/PushProx.svg?style=shield)](https://circleci.com/gh/prometheus-community/PushProx)

Refer to original repository for conceptual understanding. This is a modification for purposes of using Envoy and sending request through a tunnel. Implementation of HTTP Connect taken from [![Here](https://github.com/kubernetes-sigs/apiserver-network-proxy/blob/master/cmd/client/main.go).

There are two steps to this process. The goal is to run pushprox in Kubernetes.

The three examples/tests I used here were:
1. PushProx_DockerTest - I got PushProxy (client and proxy) and Prometheus running in docker. Then I used the images in Docker and ran it in AKS.
2. PushProx_BinaryTest - I made images for the binaries (client and proxy). In Docker, only the Prometheus was actually running. I pulled the binaries in AKS via yaml files and used the args in yaml files to run PushProxy.
3. PushProxy_StandaloneTest - Basically used the PushProx_BinaryTest yaml files but changed main.go in client and configured everything for the standalone environment.

## File Explanation
There are the three "Test" directories. Depending on the test you want to run, you'll need to replace the files in the main directory and run ```make build```. Furthermore, for the first two tests (DockerTest and BinaryTest), you'll need to replace client's main.go file with the original repository since the current one is configured for the standalone environment. This includes docker-compose.yaml, prometheus.yml, Dockerfile.

## Build and Push Docker Images
Run ```make build``` in main directory to make the binaries. I believe the binaries should be in the main directory and will be named pushprox-client and pushprox-proxy. Make sure to change main.go in client according to your desired test.

Run ```docker-compose up -d```. Note that if you have current images or containers running, you'll have to delete them before this command in order to have the new changes.

Login to your acr registry. ```az acr login -n $acr_name```
Docker tag images: ```docker tag pushprox_client $acr_name.azurecr.io/pushprox_client``` (do the same for pushprox_proxy and pushprox_prometheus)
Docker push images: ```docker push $acr_name.azurecr.io/pushprox_client```

## Run in Azure Kubernetes Service
Pretty straightforward - deploy the yaml files in the directory AKS_Deployment in your desired test directory.
