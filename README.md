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
    image: ligfx/k3d-registry-dockerd:v0.4
    proxy:
      remoteURL: "*"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
HERE
k3d cluster create mytest --config "$configfile"
```

### Podman

You can follow [this](https://k3d.io/v5.6.3/usage/advanced/podman/) guide on configuring podman to be usable by k3d.

<details>
<summary>Tested configuration</summary>
<br>

``` sh
# enabling rootless podman socket
systemctl --user enable --now podman.socket

# requirements for cgroupsv2 in rootless mode
mkdir -p /etc/systemd/system/user@.service.d
cat > /etc/systemd/system/user@.service.d/delegate.conf <<EOF
[Service]
Delegate=cpu cpuset io memory pids
EOF

# reload systemd daemon to pick up above config
systemctl daemon-reload

# symlink podman socket to docker and create "k3d" network in podman for DNS
sudo ln -s $XDG_RUNTIME_DIR/podman/podman.sock /var/run/docker.sock
podman network create k3d
```
</details>

Create passthrough registry, port, volume for cache and name of registry can be changed in below command
``` sh
k3d registry create -i ligfx/k3d-registry-dockerd:v0.4 -p 5000 \
  -v /var/run/docker.sock:/var/run/docker.sock --proxy-remote-url "*" \
  -v k3d-cache:/cache --default-network k3d registry
```

Create registry config as below, replace `registry` with a persistent path if required, in the endpoint `k3d-` prefix has to be added to the registry name created in above command
``` sh
registry=$(mktemp)
cat << HERE > "$registry"
mirrors:
  "*":
    endpoint:
      - "http://k3d-registry:5000"
HERE
```

From now on, for every new cluster you can supply registry details and pulling of images will be proxied by the registry to podman running on host.
``` sh
k3d cluster create --registry-use k3d-registry:5000 \
  --registry-config $registry \
  --k3s-arg '--kubelet-arg=feature-gates=KubeletInUserNamespace=true@all' \
  <NAME>
```
Please replace name of the registry if required, since we are running `k3d` in userspace, extra `--k3s-arg` needs to be supplied.
