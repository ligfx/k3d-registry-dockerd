#!/bin/sh

set -euxo pipefail

docker build . -t ligfx/my-registry-shim

k3d cluster delete mytest || true
docker container rm -f my-registry || true
docker network rm k3d-mytest || true

docker network create k3d-mytest
docker run -p 49156:5000 --name my-registry --network k3d-mytest -d -v /var/run/docker.sock:/var/run/docker.sock ligfx/my-registry-shim 

k3d cluster create mytest --registry-config registry.yml --network k3d-mytest
# kubectl run test --image ligfx/my-other-image 

docker logs -f my-registry