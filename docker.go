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

type DockerImagePullStatus struct {
	Status string
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
	return client, nil
}

func (client *DockerClient) ImageList(reference string) ([]DockerImageInfo, error) {
	// TODO: use contexts
	resp, err := client.httpClient.Get(fmt.Sprintf("http://unix/v1.45/images/json?filters={\"reference\":[\"%s\"]}", reference))
	if err != nil {
		return nil, err
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var images []DockerImageInfo
	err = json.Unmarshal(content, &images)
	return images, err
}

func (client *DockerClient) ImagePull(reference string, statusHandler func(statusMessage string)) error {
	// TODO: use contexts
	resp, err := client.httpClient.Post(
		fmt.Sprintf("http://unix/v1.45/images/create?fromImage=%s", reference),
		"text/plain",
		nil)
	if err != nil {
		return err
	}
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

func (client *DockerClient) ImageExport(ctx context.Context, reference string) (*tar.Reader, error) {
	// TODO: use contexts
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://unix/v1.45/images/%s/get", reference), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		content, _ := io.ReadAll(resp.Body)
		return nil, errors.New(fmt.Sprint(resp.Status, ": ", string(content)))
	}

	return tar.NewReader(resp.Body), nil
}

func (client *DockerClient) Close() {
	client.httpClient.CloseIdleConnections()
}
