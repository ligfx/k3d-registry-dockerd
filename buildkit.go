package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	controlapi "github.com/moby/buildkit/api/services/control"
	buildkitsession "github.com/moby/buildkit/session"
)

func withBuildkitSession(ctx context.Context, f func(*client.Client, *buildkitsession.Session) error) error {
	return withDockerClient(func(c *client.Client) error {
		s, err := buildkitsession.NewSession(ctx, "k3d-registry-dockerd")
		if err != nil {
			return err
		}

		dialSession := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return c.DialHijack(ctx, "/session", proto, meta)
		}

		go func() {
			if err := s.Run(ctx, dialSession); err != nil {
				// TODO: cancel the context passed to ImageBuild?
				log.Printf("Error in buildkit session: %v", err)
			}
		}()
		defer s.Close()

		return f(c, s)
	})
}

func createTarredDockerfile(content string) (bytes.Buffer, error) {
	b := []byte(content)

	var buf bytes.Buffer
	t := tar.NewWriter(&buf)
	err := t.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(b))})
	if err != nil {
		return bytes.Buffer{}, err
	}
	_, err = t.Write(b)
	if err != nil {
		return bytes.Buffer{}, err
	}
	err = t.Close()
	if err != nil {
		return bytes.Buffer{}, err
	}
	return buf, nil
}

func BuildkitForceDockerPull(ctx context.Context, reference string) error {
	return withBuildkitSession(ctx, func(c *client.Client, s *buildkitsession.Session) error {
		// build a temporary image using BuildKit, which will force Docker to pull
		// all of the blobs for the parent image.

		// // temporary tag that we'll delete afterwards
		// randomId := func() string {
		// 	buf := make([]byte, 16)
		// 	_, err := rand.Read(buf)
		// 	if err != nil {
		// 		panic(err)
		// 	}
		// 	return hex.EncodeToString(buf)
		// }
		// tempTag := fmt.Sprintf("ligfx/k3d-registry-dockerd-temp-%s", randomId())

		// create a tarball containing a Dockerfile with only the contents "FROM $imagename"
		buf, err := createTarredDockerfile(fmt.Sprintf("FROM %s", reference))
		if err != nil {
			return fmt.Errorf("couldn't tar Dockerfile: %w", err)
		}

		// start a BuildKit build
		resp, err := c.ImageBuild(ctx, &buf, types.ImageBuildOptions{
			// Tags:      []string{tempTag},
			SessionID: s.ID(),
			Version:   types.BuilderBuildKit,
		})
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// delete the resulting temporary image if BuildKit gives us an image id
		var imageId string
		defer func() {
			if imageId == "" {
				return
			}
			deleted, err := c.ImageRemove(ctx, imageId, image.RemoveOptions{})
			if err != nil {
				log.Printf("Error deleting %s: %s", imageId, err)
				return
			}
			for _, d := range deleted {
				if d.Deleted != "" {
					log.Printf("Deleted temporary image %s", d.Deleted)
				}
				if d.Untagged != "" {
					log.Printf("Untagged %s", d.Untagged)
				}
			}
		}()

		// parse and log BuildKit status messages which are in some binary encoding
		scanner := bufio.NewScanner(resp.Body)
		seenMessages := map[string]bool{}
		for scanner.Scan() {
			// ignore any JSON errors
			var msg jsonmessage.JSONMessage
			_ = json.Unmarshal(scanner.Bytes(), &msg)

			// grab the built image ID so we can delete it afterwards
			if msg.ID == "moby.image.id" {
				var result types.BuildResult
				err := json.Unmarshal(*msg.Aux, &result)
				if err != nil {
					log.Printf("error parsing %q: %s", scanner.Text(), err)
				} else {
					imageId = result.ID
				}
			}

			// if not a trace message, just print the original line of text
			if msg.ID != "moby.buildkit.trace" {
				log.Printf("%s", scanner.Text())
				continue
			}

			// parse and print StatusResponse messages which have top-level vertexes,
			// vertexstatus updates for vertexes, and logs and warnings.
			var dt []byte
			err := json.Unmarshal(*msg.Aux, &dt)
			if err != nil {
				log.Printf("%s", err)
				continue
			}
			var resp controlapi.StatusResponse
			err = (&resp).UnmarshalVT(dt)
			if err != nil {
				log.Printf("%s", err)
				continue
			}
			for _, vertex := range resp.Vertexes {
				out := fmt.Sprintf("\"Name\":%q", vertex.Name)
				if vertex.Error != "" {
					out = fmt.Sprintf("%s,\"Error\":%q", out, vertex.Error)
				}
				out = fmt.Sprintf("{\"id\":\"moby.buildkit.trace\",\"Vertex\":{%s}}", out)
				if _, ok := seenMessages[out]; !ok {
					log.Printf("%s", out)
					seenMessages[out] = true
				}
			}
			for _, status := range resp.Statuses {
				out := fmt.Sprintf("\"ID\":%q", status.ID)
				if status.Name != "" {
					out = fmt.Sprintf("%s,\"Name\":%q", out, status.Name)
				}
				if status.Current != 0 || status.Total != 0 {
					out = fmt.Sprintf("%s,\"Current\":%v,\"Total\":%v", out, status.Current, status.Total)
				}
				out = fmt.Sprintf("{\"id\":\"moby.buildkit.trace\",\"VertexStatus\":{%s}}", out)
				if _, ok := seenMessages[out]; !ok {
					log.Printf("%s", out)
					seenMessages[out] = true
				}
			}
			for _, msg := range resp.Logs {
				log.Printf("%v", msg)
			}
			for _, warning := range resp.Warnings {
				log.Printf("%v", warning)
			}
		}
		return scanner.Err()
	})
}
