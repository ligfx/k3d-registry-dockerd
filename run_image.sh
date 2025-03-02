#!/bin/sh

set -euxo pipefail

go fmt .

cat Dockerfile |
  sed 's/^RUN /RUN --mount=type=cache,target=\/root\/.cache\/go-build --mount=type=cache,target=\/go\/pkg\/mod /g' |
  docker build . -f - -t ligfx/k3d-registry-dockerd

k3d cluster delete mytest || true

cleanup() {
  k3d cluster delete mytest
}
trap cleanup EXIT

configfile=$(mktemp)
cat << HERE > "$configfile"
apiVersion: k3d.io/v1alpha5
kind: Simple
registries:
  create:
    image: ligfx/k3d-registry-dockerd
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
options:
  k3s:
    extraArgs:
      - arg: "--disable-default-registry-endpoint"
        nodeFilters:
          - "all:*"
HERE
k3d cluster create mytest --config "$configfile"

kubectl create deployment image-name-without-namespace --image=busybox -- sh -c 'while true; do echo "hello world" && sleep 5; done'

docker logs -f k3d-mytest-registry
