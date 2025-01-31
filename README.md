# k3d-registry-dockerd

Provides an [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)-compliant(ish) registry
that proxies all requests to a dockerd process on the same system.

## Why?

Mainly for use with [k3d](https://k3d.io/). I got frustrated with k3d running into rate limits pulling images
from docker.io everytime, since it can't use the local Docker image cache directly. k3d-registry-dockerd can
be used by k3d and will transparently use your local Docker image cache, preventing rate limiting issues
and potentially speeding up your Pod deploys.

## Usage

Run k3d like this:

```sh
configfile=$(mktemp)
cat << HERE > "$configfile"
apiVersion: k3d.io/v1alpha5
kind: Simple
registries:
  create:
    image: ligfx/k3d-registry-dockerd:v0.7
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
HERE
k3d cluster create mytest --config "$configfile"
```

Or, to use a separate but-still-k3d-managed registry:

```sh
k3d registry create -i ligfx/k3d-registry-dockerd:v0.7 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  myregistry
configfile=$(mktemp)
cat << HERE > "$configfile"
apiVersion: k3d.io/v1alpha5
kind: Simple
registries:
  use:
    - k3d-myregistry:5000
  config: |
    mirrors:
      "*":
        endpoint:
          - "http://k3d-myregistry:5000"
HERE
k3d cluster create mytest --config "$configfile"
```