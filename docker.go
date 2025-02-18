package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/moby/buildkit/session"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
)

type DockerImageInfo struct {
	Id string
}

type DockerClient struct {
	httpClient http.Client
}

func NewDockerClient() (*DockerClient, error) {
	client := new(DockerClient)
	client.httpClient = http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
	}

	// TODO: try to do version negotiation
	// - check /_ping for the API-Version header?
	// - check /version for supported range (can you call this without a version?)
	// - if it's not v1.44, throw a big warning but let the user keep going

	return client, nil
}

func (client *DockerClient) ImageList(ctx context.Context, reference string) ([]DockerImageInfo, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		"GET",
		fmt.Sprintf("http://unix/v1.44/images/json?filters={\"reference\":[\"%s\"]}", reference),
		nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var images []DockerImageInfo
	err = json.Unmarshal(content, &images)
	if err != nil {
		err = fmt.Errorf("%w while parsing: %v", err, string(content))
	}
	return images, err
}

func (client *DockerClient) ImagePull(ctx context.Context, reference string, statusHandler func(statusMessage string)) error {
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf("http://unix/v1.44/images/create?fromImage=%s", reference),
		nil)
	if err != nil {
		return err
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil
	}

	if resp.StatusCode != 200 {
		content, _ := io.ReadAll(resp.Body)
		return errors.New(fmt.Sprint(resp.Status, ": ", string(content)))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if statusHandler != nil {
			statusHandler(scanner.Text())
		}
	}
	return scanner.Err()
}

func randomId() string {
	buf := make([]byte, 16)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func (myClient *DockerClient) ImageActuallyPull(ctx context.Context, reference string) error {
	// build a temporary image using buildx, which will force Docker to pull
	// all of the blobs for the parent image.
	// an alternative implementation would be to return the temporary image directly
	// because it seems to have all of the same blobs. however, this would need to
	// implement some GRPC methods which seems more complicated.

	tempTag := fmt.Sprintf("ligfx/k3d-registry-dockerd-temp-%s", randomId())

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	s, err := session.NewSession(ctx, "k3d-registry-dockerd")
	if err != nil {
		return err
	}
	log.Printf("Starting buildx session %s", s.ID())

	dialSession := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		return cli.DialHijack(ctx, "/session", proto, meta)
	}

	go func() {
		if err := s.Run(ctx, dialSession); err != nil {
			log.Printf("Error in buildkit session: %w", err)
		}
	}()
	defer s.Close()

	dockerFile := []byte(fmt.Sprintf("FROM %s", reference))
	var buf bytes.Buffer
	t := tar.NewWriter(&buf)
	err = t.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(dockerFile))})
	if err != nil {
		return err
	}
	_, err = t.Write(dockerFile)
	if err != nil {
		return err
	}
	err = t.Close()
	if err != nil {
		return err
	}

	resp, err := cli.ImageBuild(ctx, &buf, types.ImageBuildOptions{
		Tags:      []string{tempTag},
		SessionID: s.ID(),
		Version:   types.BuilderBuildKit,
	})
	if err != nil {
		var content []byte
		if resp.Body != nil {
			content, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		return errors.New(fmt.Sprintf("%w: %q", err, string(content)))
	}

	defer func() {
		req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("http://unix/v1.44/images/%s", tempTag), nil)
		if err != nil {
			log.Printf("Error deleting %s: %w", tempTag, err)
			return
		}
		resp, err := myClient.httpClient.Do(req)
		if err != nil {
			log.Printf("Error deleting %s: %w", tempTag, err)
			return
		}
		var content []byte
		if resp.Body != nil {
			defer resp.Body.Close()
			content, _ = io.ReadAll(resp.Body)
		}
		if resp.StatusCode != 200 {
			log.Printf("Error deleting %s: HTTP %v: %s", tempTag, resp.Status, string(content))
			return
		}
		log.Printf("Deleted %s: %s", tempTag, string(content))
	}()

	if resp.Body != nil {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			log.Printf("%s", scanner.Text())
		}
		return scanner.Err()
	}
	return nil // I guess?
}

func (client *DockerClient) ImageExport(ctx context.Context, reference string, tarballHandler func(*tar.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://unix/v1.44/images/%s/get", reference), nil)
	if err != nil {
		return err
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		content, _ := io.ReadAll(resp.Body)
		return errors.New(fmt.Sprint(resp.Status, ": ", string(content)))
	}

	return tarballHandler(tar.NewReader(resp.Body))
}

func (client *DockerClient) Close() {
	client.httpClient.CloseIdleConnections()
}
