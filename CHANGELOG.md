# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)

## [Unreleased]
- Doesn't keep partial corrupted files when encountering errors writing manifests or blobs. `copyToFile` now first writes to a temporary file in the same directory as the destination, and then renames it to the actual destination filename only if the writes succeeds entirely. Solves [#20 Inconsistent cache state on http error](https://github.com/ligfx/k3d-registry-dockerd/issues/20).
- Doesn't ignore `io.ErrUnexpectedEOF` errors in `copyToFile`.
- Supports pulling private images via secrets passed in from Kubernetes. Attemping to pull a non-existent image will now return a 401 instead of a 404 unless authorization credentials have been supplied. Authorization credentials are passed directly through to the Docker client. Solves [#21 Support authenticated container registry](https://github.com/ligfx/k3d-registry-dockerd/issues/21).

## [0.8] - 2025-03-02

- Reads `REGISTRY_HTTP_ADDR` from the environment like the official registry image. This fixes an issue on k3d 5.8.2 where [custom image registries that don't support `REGISTRY_HTTP_ADDR` no longer work](https://github.com/k3d-io/k3d/issues/1552). k3d 5.8.3 [reverted this behavior](https://github.com/k3d-io/k3d/issues/1552#issuecomment-2661051486), but once it comes back this may also solve [#10 k3d-registry-dockerd doesn't respect port number passed in with "k3d registry create"](https://github.com/ligfx/k3d-registry-dockerd/issues/10).
- Changes the `-port` CLI option to `-addr`, to better match the environment variable.
- Bugfix: don't return 404 the first time exporting an image specified in `domain/name@sha256:digest` format. Support for these image references was added in version 0.6 and this bug was introduced in version 0.7.
- Bugfix: don't download multiple versions of the same image in parallel. This could have caused issues with shared blobs overwriting each other. Different images altogether are still downloaded in parallel, as introduced in version 0.5.
- Supports uploading blobs and manifests, for use with tools like Tilt. Fixes [#12 Attempts to push into the registry returns HTTP 404](https://github.com/ligfx/k3d-registry-dockerd/issues/12).
- Support images referenced without a namespace, like `alpine:latest` or `busybox:latest`. These are passed to the OCI Registry API as `library/name:tagOrDigest`, but need to be passed to Docker without the `library/` prefix. Fixes [#17 Support images specified without namespace](https://github.com/ligfx/k3d-registry-dockerd/issues/17).
- Logs an error and returns 404 when exporting an image referenced directly by digest and the resulting export does not contain the referenced blob. This is known to happen when not using Docker's containerd image store. See [#14 `docker save` returns incorrect digests for digest-referenced images when not using containerd storage](https://github.com/ligfx/k3d-registry-dockerd/issues/14).
- Runs BuildKit to try to fix images that are exported with missing layer blobs. If the export is still missing blobs, logs an error and returns 404. This is known to happen when using Docker's containerd image store and pulling images that share layers. See [#13 `docker save` sometimes returns images missing blobs](https://github.com/ligfx/k3d-registry-dockerd/issues/13) and [moby/moby#49473 `docker save` with containerd snapshotter returns OCI images missing all blob layers when image shares layers with another image](https://github.com/moby/moby/issues/49473)

## [0.7] - 2025-01-31

- Returns manifest matching digest instead of `index.json` when images are specified in `domain/name@sha256:digest` format. This would result in a container with a different reported imageID than the one specified.
- Returns manifest list instead of `index.json` when images are specified in `domain/name:version` format. This would result in a container with a different reported imageID than what was visible in Docker.
- Parses actual manifest mediaType from JSON and return it in the `Content-Type` header, rather than guessing.

## [0.6] - 2024-11-11

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