package main

import (
	"archive/tar"
	"bufio"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"strings"
)

func withDockerClient(f func(*client.Client) error) error {
	// TODO: turn this into a connection pool later if needed

	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("error creating Docker client: %w", err)
	}
	defer c.Close()

	// TODO: throw a big warning if v1.44 is not available but let the user keep going?
	// that seems to be the first version that exports images with OCI layout

	return f(c)
}

func withDockerClientValue[T any](f func(*client.Client) (T, error)) (T, error) {
	var result T
	err := withDockerClient(func(c *client.Client) error {
		var err error
		result, err = f(c)
		return err
	})
	return result, err
}

func DockerGetClientVersion(ctx context.Context) (string, error) {
	return withDockerClientValue(func(c *client.Client) (string, error) {
		return c.ClientVersion(), nil
	})
}

func DockerGetDaemonHost(ctx context.Context) (string, error) {
	return withDockerClientValue(func(c *client.Client) (string, error) {
		return c.DaemonHost(), nil
	})
}

type DockerInfo struct {
	ApiVersion         string
	DaemonHost         string
	ServerVersion      string
	ServerOSType       string
	ServerArchitecture string
}

func DockerGetInfo(ctx context.Context) (DockerInfo, error) {
	return withDockerClientValue(func(c *client.Client) (DockerInfo, error) {
		info := DockerInfo{
			ApiVersion:         c.ClientVersion(),
			DaemonHost:         c.DaemonHost(),
			ServerVersion:      "unknown",
			ServerOSType:       "unknown",
			ServerArchitecture: "unknown",
		}

		serverInfo, err := c.Info(ctx)
		if err != nil {
			return info, nil
		}
		info.ServerVersion = serverInfo.ServerVersion
		info.ServerOSType = serverInfo.OSType
		info.ServerArchitecture = serverInfo.Architecture

		return info, nil
	})
}

func DockerImageInspect(ctx context.Context, reference string) (*image.InspectResponse, error) {
	return withDockerClientValue(func(c *client.Client) (*image.InspectResponse, error) {
		image, err := c.ImageInspect(ctx, reference)
		if err != nil {
			if errdefs.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return &image, nil
	})
}

type ImageAuthConfig = registry.AuthConfig

func DockerImagePull(ctx context.Context, reference string, auth *ImageAuthConfig, statusHandler func(statusMessage string)) (bool, error) {
	return withDockerClientValue(func(c *client.Client) (bool, error) {
		opts := image.PullOptions{}
		if auth != nil {
			authstring, err := registry.EncodeAuthConfig(*auth)
			if err != nil {
				return false, err
			}
			opts.RegistryAuth = authstring
		}

		resp, err := c.ImagePull(ctx, reference, opts)
		if resp != nil {
			defer resp.Close()
		}
		if err != nil {
			if errdefs.IsNotFound(err) {
				if auth == nil {
					// many repositories, including docker.io, do not provide enough information
					// to know if an image doesn't exist or if we just aren't authorized to know
					// it exists, and will return a 401 Unauthorized instead of a 404 Not Found.
					// if the caller of this function didn't provide auth, then return an unauthorized
					// error so k8s knows to try again with any auth secrets it may have.
					return false, fmt.Errorf("pull access denied, image does not exist or may require authorization")
				} else {
					return false, nil
				}
			}
			return false, fmt.Errorf("error pulling image %s: %w", reference, err)
		}

		scanner := bufio.NewScanner(resp)
		for scanner.Scan() {
			if statusHandler != nil {
				statusHandler(scanner.Text())
			}
		}
		return true, scanner.Err()
	})
}

func DockerImageExport(ctx context.Context, reference string, tarballHandler func(*tar.Reader) error) error {
	return withDockerClient(func(c *client.Client) error {
		resp, err := c.ImageSave(ctx, []string{reference})
		if resp != nil {
			defer resp.Close()
		}
		if err != nil {
			return fmt.Errorf("error exporting image %s: %w", reference, err)
		}
		return tarballHandler(tar.NewReader(resp))
	})
}

func IsUnauthorizedError(err error) bool {
	// The Docker errdefs package (actually a shim over the containerd errdefs package)
	// provides an IsUnauthorized function that theoretically reports if an error is
	// an unauthorized error.
	if errdefs.IsUnauthorized(err) {
		return true
	}

	// However, in practice, it doesn't seem to work – the error returned when trying
	// to pull an image which needs authorization is a generic errdefs.errSystem and
	// errdefs.IsUnauthorized returns false. So, we need to guess based on the contents
	// of the following error messages instead (as of Docker version 28.1.1):
	//
	//     Error response from daemon: Head "${reference}": no basic auth credentials
	//
	//     Error response from daemon: failed to resolve reference "${reference}":
	//     pull access denied, repository does not exist or may require authorization:
	//     authorization failed: no basic auth credentials
	//
	//     Error response from daemon: error from registry: failed to resolve reference
	//     "${reference}": failed to authorize: failed to fetch oauth token: unexpected
	//     status from GET request to ${registry_server_auth_url}: 401 Unauthorized
	//
	//     Error response from daemon: unknown: failed to resolve reference "${reference}":
	//     unexpected status from HEAD request to ${registry_server_url}: 401 Unauthorized
	//
	if strings.Contains(err.Error(), "no basic auth credentials") ||
		strings.Contains(err.Error(), "authorization") ||
		strings.Contains(err.Error(), "401 Unauthorized") {
		return true
	}

	return false
}
