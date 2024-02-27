#!/bin/sh

set -euxo pipefail

docker build . -t ligfx/k3d-registry-dockerd

k3d cluster delete mytest || true
docker container rm -f my-registry || true
docker network rm k3d-mytest || true

docker network create k3d-mytest
docker run -p 49156:5000 --name my-registry --network k3d-mytest -d -v /var/run/docker.sock:/var/run/docker.sock -v /var/lib/docker:/var/lib/docker ligfx/k3d-registry-dockerd

k3d cluster create mytest --registry-config registry.yml --network k3d-mytest \
    --servers "1" --servers-memory "1.5g" \
    --agents "3" --agents-memory "4g" \
    -i 'docker.io/rancher/k3s:v1.27.11-rc1-k3s1'
# kubectl run test --image ligfx/my-other-image 

docker logs -f my-registry