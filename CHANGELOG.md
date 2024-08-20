# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)

## [Unreleased]
- Supports images specified in `domain/name@sha256:digest` format (such as `registry.k8s.io/ingress-nginx/controller@sha256:d5f8217feea...`)

## [0.5] - 2024-07-29

- Downloads multiple images in parallel, which improves cluster startup time. Uses [golang.org/x/sync's singleflight package](https://pkg.go.dev/golang.org/x/sync@v0.7.0/singleflight) to coalesce multiple requests for the same image and ensure that downloads don't interfere with one another.
- Logs errors when handling HTTP requests, rather than just sending them to the client. This makes it far easier to debug when things go wrong.
- Correctly return errors when copying files to local cache
- Improves error messages when trying to communicate with the Docker daemon by including JSON content that failed to unmarshal in the error message
- Lower required Docker engine API version to v1.44 from v1.45. This seems to correspond with the earliest engine version that supported fully-OCI-compliant image export.
- Bugfix: close HTTP request bodies correctly

## [0.4] - 2024-05-25

- Rewrite the whole thing in [Go](https://go.dev/). It seems much faster! And this opens up possibilities of handling requests in parallel, as well (currently limited to one request at time, since otherwise you get a thundering herd of calls to the Docker API). Maybe one day this could get integrated into [k3d](https://k3d.io/) itself
- Allow image names that don't have a slash in them. Useful if you're using any raw base images for your pods, e.g. `golang:1.22.3-alpine3.20` or `alpine:3.20.0`.

## [0.3] - 2024-05-25

- Fix error "Not supported URL scheme http+docker" by updating Python requirement [`docker`](https://pypi.org/project/docker/) from 7.0.0 to 7.1.0

## [0.2] - 2024-05-25

- Fix for [#2 avoid using cache when the image on the host have change](https://github.com/ligfx/k3d-registry-dockerd/issues/2). This allows you to use k3d-registry-dockerd with locally-built development images that may change content without changing their version.

## [0.1] - 2024-02-27

Initial release!

k3d-registry-dockerd provides an [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)-compliant(ish)
registry that proxies all requests to a dockerd process on the same system.

I got frustrated with [k3d](https://k3d.io/) running into rate limits pulling images from docker.io
everytime, since it can't use the local Docker image cache directly. k3d-registry-dockerd can be
used by k3d and will transparently use your local Docker image cache, preventing rate limiting issues
and potentially speeding up your Pod deploys.

Run with k3d like this:

```sh
configfile=$(mktemp)
cat << HERE > "$configfile"
apiVersion: k3d.io/v1alpha5
kind: Simple
registries:
  create:
    image: ligfx/k3d-registry-dockerd:v0.1
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
HERE
k3d cluster create mytest --config "$configfile"
```