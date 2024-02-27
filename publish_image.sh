#!/bin/sh

set -euxo pipefail

docker buildx build . -t ligfx/k3d-registry-dockerd --platform linux/arm64,linux/amd64 --push