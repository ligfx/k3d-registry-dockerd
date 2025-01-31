#!/bin/sh

set -euxo pipefail

go fmt .

cat Dockerfile |
  sed 's/^RUN /RUN --mount=type=cache,target=\/root\/.cache\/go-build --mount=type=cache,target=\/go\/pkg\/mod /g' |
  docker build . -f - -t ligfx/k3d-registry-dockerd

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
      # - /var/lib/docker:/var/lib/docker
HERE
k3d cluster create mytest --config "$configfile"

docker logs -f k3d-mytest-registry
