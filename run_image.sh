#!/bin/sh

set -euxo pipefail

docker build . -t ligfx/k3d-registry-dockerd

k3d cluster delete mytest || true

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
      - /var/lib/docker:/var/lib/docker
HERE
k3d cluster create mytest --config "$configfile"

docker logs -f k3d-mytest-registry
