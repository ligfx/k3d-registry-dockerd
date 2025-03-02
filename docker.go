package main

import (
	"archive/tar"
	"bufio"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
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

func DockerImageList(ctx context.Context, reference string) ([]image.Summary, error) {
	return withDockerClientValue(func(c *client.Client) ([]image.Summary, error) {
		filt := filters.NewArgs()
		filt.Add("reference", reference)
		return c.ImageList(ctx, image.ListOptions{Filters: filt})
	})
}

func DockerImagePull(ctx context.Context, reference string, statusHandler func(statusMessage string)) (bool, error) {
	return withDockerClientValue(func(c *client.Client) (bool, error) {
		resp, err := c.ImagePull(ctx, reference, image.PullOptions{})
		if resp != nil {
			defer resp.Close()
		}
		if err != nil {
			if errdefs.IsNotFound(err) {
				return false, nil
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
