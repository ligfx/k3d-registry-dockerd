#!/bin/sh

# This is a simple script for testing k3d-registry-dockerd private registry
# support. It spins up a local registry with authorization required, pushes
# in a Docker image, and then tries to pull that image back from the k8s cluster.

set -euo pipefail

cd "$(dirname "$0")"

registry_server_pid=
cleanup() {
    if test "$registry_server_pid"; then
        if ps -p "$registry_server_pid" > /dev/null; then
            echo kill "$registry_server_pid"
            kill "$registry_server_pid"
        fi
    fi
    echo docker image rm localhost:15000/busybox:latest
    docker image rm localhost:15000/busybox:latest || true
}
trap cleanup EXIT

# generate htpasswd file
if ! test -f "htpasswd"; then
    docker run --rm --entrypoint htpasswd httpd:2 -Bbn "myusername" "mypassword" > htpasswd 
fi

# start protected registry
docker run \
    -p 15000:5000 \
    -e "REGISTRY_AUTH=htpasswd" \
    -e "REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm" \
    -e REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd \
    -e "OTEL_TRACES_EXPORTER=none" \
    -e "REGISTRY_LOG_LEVEL=info" \
    -v "$(pwd)/htpasswd:/auth/htpasswd:ro" \
    registry:3 &

registry_server_pid=$!

for i in {0..30}; do
    sleep 0.1
    if ! ps -p "$registry_server_pid" > /dev/null; then
        exit 1
    fi
done

# export an image to the registry
echo mypassword | docker login localhost:15000 --username myusername --password-stdin
docker image tag busybox:latest localhost:15000/busybox:latest
docker push localhost:15000/busybox:latest
docker image rm localhost:15000/busybox:latest
docker logout localhost:15000

# set up k8s secret and create a deployment using the private image
if kubectl get secrets | grep -q private-registry-credentials; then
    kubectl delete secret private-registry-credentials
fi
kubectl create secret docker-registry private-registry-credentials \
    --docker-username=myusername \
    --docker-password=mypassword \
    --docker-server=localhost:15000
kubectl apply -f private-registry-deployment.yaml

# keep running until registry exits
wait "$registry_server_pid"