#!/bin/sh

set -euo pipefail

versions=$(git tag --points-at HEAD | grep '^v')
if test "$(git rev-parse --abbrev-ref HEAD)" = "main"; then
    versions="$versions latest"
fi
if test -z "$versions"; then
    echo >&2 "Don't know which versions to push!"
    exit 1
fi
echo "Will publish: $versions"
image_tags=""
for ver in $versions; do
    image_tags="${image_tags} -t ligfx/k3d-registry-dockerd:$ver"
done
docker buildx build . $image_tags --platform linux/arm64,linux/amd64 --push