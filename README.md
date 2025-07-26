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
    image: ligfx/k3d-registry-dockerd:latest
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
HERE
k3d cluster create mytest --config "$configfile"
```

Or, to use a separate but-still-k3d-managed registry:

```sh
k3d registry create -i ligfx/k3d-registry-dockerd:latest \
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

### Images may be exported with missing layer blobs when using Docker's containerd image store and pulling images that share layers

When Docker is using the containerd image store, images may be exported without any layer blobs.
This seems to happen when images share layers with another image that has already been pulled,
and containerd discards the blobs.

k3d-registry-dockerd will detect this situation and attempt to automatically fix the containerd
image store by building a new child image using the BuildKit API (à la
`echo "FROM $image" | docker buildx build -`), which makes containerd fetch the blobs.

If the export is still missing blobs, k3d-registry-dockerd will log an error and return an HTTP
404 Not Found status telling Kubernetes to try another registry.

If building a child image through BuildKit did not fix the issue, containerd can be explicitly
told to fetch the blobs via the containerd API or by using a containerd client, such as with
`ctr content fetch $image` or `nerdctl image pull --unpack=false $image`.

See [#13 `docker save` sometimes returns images missing blobs](https://github.com/ligfx/k3d-registry-dockerd/issues/13) and [moby/moby#49473 `docker save` with containerd snapshotter returns OCI images missing all blob layers when image shares layers with another image](https://github.com/moby/moby/issues/49473)

## Troubleshooting and reporting bugs

k3d-registry-dockerd outputs detailed logs on all image requests and interactions with Docker. When reporting an issue, please provide these logs, which you can get by running `docker logs $registry_container_name`.

## ECR Private Repository Support

k3d-registry-dockerd includes built-in support for AWS ECR (Elastic Container Registry) private repositories. ECR support is automatically included in all builds - no special image or configuration is needed beyond providing AWS credentials.

### Prerequisites

1. **AWS CLI or credentials configured** on your host system
2. **ECR credential helper** installed and configured (this is included in the Docker image)
3. **AWS credentials** available via one of the standard methods:
   - Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`)
   - AWS profile (`AWS_PROFILE`)
   - AWS credentials file (`~/.aws/credentials`)
   - IAM roles (for EC2/ECS instances)

### Usage with ECR

To use k3d-registry-dockerd with ECR private repositories, mount your AWS credentials and set the appropriate environment variables:

```sh
docker run -d \
  --name k3d-ecr-registry \
  --network k3d-mycluster \
  --restart unless-stopped \
  -p 5000:5000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v $HOME/.aws:/root/.aws:ro \
  -v $HOME/.docker:/root/.docker:ro \
  -e AWS_PROFILE=your-profile \
  -e HOME=/root \
  ligfx/k3d-registry-dockerd:latest
```

Or with explicit AWS credentials:

```sh
docker run -d \
  --name k3d-ecr-registry \
  --network k3d-mycluster \
  --restart unless-stopped \
  -p 5000:5000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e AWS_ACCESS_KEY_ID=your-access-key \
  -e AWS_SECRET_ACCESS_KEY=your-secret-key \
  -e AWS_REGION=us-west-2 \
  ligfx/k3d-registry-dockerd:latest
```

With k3d configuration:

```sh
configfile=$(mktemp)
cat << HERE > "$configfile"
apiVersion: k3d.io/v1alpha5
kind: Simple
registries:
  create:
    image: ligfx/k3d-registry-dockerd:latest
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - $HOME/.aws:/root/.aws:ro
      - $HOME/.docker:/root/.docker:ro
    env:
      - AWS_PROFILE=your-profile
      - HOME=/root
HERE
k3d cluster create mycluster --config "$configfile"
```

### How ECR Authentication Works

1. **Automatic Detection**: The registry automatically detects ECR repository URLs (format: `*.dkr.ecr.*.amazonaws.com`)
2. **Credential Resolution**: When an ECR repository is detected, it uses the ECR credential helper to obtain authentication tokens
3. **Credential Precedence**: HTTP basic auth (if provided) takes precedence over automatic ECR authentication
4. **Fallback**: If ECR authentication fails, the registry falls back to normal Docker behavior

### ECR Repository Examples

Once configured, you can use ECR images directly in your Kubernetes manifests:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: my-app
        image: 123456789012.dkr.ecr.us-west-2.amazonaws.com/my-app:latest
```

The registry will automatically authenticate with ECR and cache the image locally for faster subsequent deployments.