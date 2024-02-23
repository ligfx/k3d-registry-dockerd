# k3d-registry-dockerd

Provides an [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)-compliant(ish) registry
that proxies all requests to a dockerd process on the same system.

## Why?

Mainly for use with [k3d](https://k3d.io/). I got frustrated with k3d running into rate limits pulling images
from docker.io everytime, since it can't use the local Docker image cache directly. k3d-registry-dockerd can
be used by k3d and will transparently use your local Docker image cache, preventing rate limiting issues
and potentially speeding up your Pod deploys.
