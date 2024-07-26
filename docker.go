package main

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
