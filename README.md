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

## Using locally-built images

To use locally-built images, simply give them a tag and reference them as normal in your Kubernetes configuration. Images should _not_ be tagged with the registry's domain.

For instance, the following should work:

```sh
docker build . -t my-image:latest
kubectl create deployment my-image --image="my-image:latest"
```

k3d-registry-dockerd also supports using tools like [Tilt](https://tilt.dev/) by accepting images pushed directly into the registry.

## Known issues

There are some known scenarios where Docker will export images that are unusable
by Kubernetes. They may have the incorrect ID, are missing blobs, etc.

### Images cannot be referenced by digest when not using Docker's containerd image store

When an image is referenced directly by digest (like `name@sha256:digest`) and Docker is
not using the containerd image store, the exported image will not contain a manifest blob
matching the expected digest. To get a usable export for these images, Docker must be
configured to use the containerd image store instead.

k3d-registry-dockerd will detect this situation, log an error, and return an HTTP 404 Not
Found status telling Kubernetes to try another registry.

See [#14 `docker save` returns incorrect digests for digest-referenced images when not using containerd storage](https://github.com/ligfx/k3d-registry-dockerd/issues/14).
