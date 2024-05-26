# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)

## [Unreleased]

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