package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sync/singleflight"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var docker *DockerClient

func handleHelloWorld(w http.ResponseWriter, req *http.Request) {
	fmt.Fprint(w, "Hello, world!\n")
}

func handleV2(w http.ResponseWriter, req *http.Request) {
	// path has to return 2xx but doesn't have to have content
}

func findImage(ctx context.Context, fullName string) (*DockerImageInfo, error) {
	images, err := docker.ImageList(ctx, fullName)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		err = docker.ImagePull(ctx, fullName, func(statusMessage string) {
			log.Println(statusMessage)
		})
		if err != nil {
			return nil, err
		}
		images, err = docker.ImageList(ctx, fullName)
		if err != nil {
			return nil, err
		}
	}
	if len(images) != 0 {
		info := new(DockerImageInfo)
		*info = images[0]
		return info, nil
	}
	return nil, nil
}

// const CACHE_DIRECTORY = "/var/lib/k3d-registry-dockerd/cache"
const CACHE_DIRECTORY = "cache"

func cachedIndexFilename(imageId string, filename string) string {
	return fmt.Sprint(CACHE_DIRECTORY, "/indexes/", imageId, "/", filename)
}

func cachedBlobFilenameForSha256(sha256 string) string {
	return fmt.Sprint(CACHE_DIRECTORY, "/blobs/sha256/", sha256)
}

func openCachedBlobForSha256(sha256 string) (io.Reader, error) {
	cachePath := cachedBlobFilenameForSha256(sha256)
	file, err := os.Open(cachePath)
	// if err == nil {
	// 	log.Print("Found ", cachePath)
	// }
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return file, err
}

func copyToFile(filename string, reader io.Reader) (int64, error) {
	err := os.MkdirAll(filepath.Dir(filename), 0777)
	if err != nil {
		return 0, err
	}
	f, err := os.Create(filename)
	if err != nil {
		return 0, err
	}
	return io.Copy(f, reader)
}

func saveOciImageToCache(imageName string, imageId string, tarball *tar.Reader) error {
	for {
		header, err := tarball.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.FileInfo().IsDir() {
			continue
		}

		var cachePath string
		if strings.HasPrefix(header.Name, "blobs/") {
			// blobs get written directly
			cachePath = fmt.Sprint(CACHE_DIRECTORY, "/", header.Name)
			// TODO: if cachePath exists already, skip it
			bytesWritten, err := copyToFile(cachePath, tarball)
			if err != nil {
				return err
			}
			log.Printf("Wrote %s (%d bytes)", cachePath, bytesWritten)
		} else {
			// text files get written twice - once with their filenames, once as content-addressable blobs
			content, err := io.ReadAll(tarball)
			if err != nil {
				return err
			}

			cachePath = cachedIndexFilename(imageId, strings.TrimPrefix(header.Name, "/"))
			bytesWritten, err := copyToFile(cachePath, bytes.NewReader(content))
			if err != nil {
				return err
			}
			log.Printf("Wrote %s (%d bytes)", cachePath, bytesWritten)

			shasumbytes := sha256.Sum256(content)
			shasum := hex.EncodeToString(shasumbytes[:])
			cachePath = cachedBlobFilenameForSha256(shasum)
			bytesWritten, err = copyToFile(cachePath, bytes.NewReader(content))
			if err != nil {
				return err
			}
			log.Printf("Wrote %s (%d bytes)", cachePath, bytesWritten)
		}
	}
}

var indexSingleFlightGroup singleflight.Group

func openIndex(ctx context.Context, fullName string) (io.Reader, error) {
	imageId, err, _ := indexSingleFlightGroup.Do(fullName, func() (any, error) {
		imageInfo, err := findImage(ctx, fullName)
		if err != nil {
			return nil, err
		}
		if imageInfo == nil {
			return nil, nil
		}
		_, err = os.Open(cachedIndexFilename(imageInfo.Id, "index.json"))
		if err == nil {
			return imageInfo.Id, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		log.Printf("Exporting Docker image %s %s", fullName, imageInfo.Id)
		err = docker.ImageExport(ctx, imageInfo.Id, func(tarball *tar.Reader) error {
			return saveOciImageToCache(fullName, imageInfo.Id, tarball)
		})

		if err != nil {
			return nil, err
		}

		return imageInfo.Id, err
	})
	if imageId == nil || err != nil {
		return nil, err
	}
	return os.Open(cachedIndexFilename(imageId.(string), "index.json"))
}

func handleBlobs(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	// name := req.PathValue("name")
	digest := req.PathValue("digest")
	// domain := req.URL.Query().Get("ns")
	// if domain == "" {
	// 	// don't support acting as own repo
	// 	http.NotFound(w, req)
	// 	return
	// }

	// TODO: check accept header?

	shasum := strings.TrimPrefix(digest, "sha256:")
	blob, err := openCachedBlobForSha256(shasum)
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	if blob == nil {
		http.NotFound(w, req)
		return
	}
	if req.Method == "GET" {
		_, err = io.Copy(w, blob)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
	}
}

func handleManifests(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	name := req.PathValue("name")
	tagOrDigest := req.PathValue("tagOrDigest")
	domain := req.URL.Query().Get("ns")
	if domain == "" {
		// don't support acting as own repo
		http.NotFound(w, req)
		return
	}

	// build the full image name, e.g. domain/namespace/image:tag
	// used for exporting the image if needed
	var fullName string
	if strings.HasPrefix(tagOrDigest, "sha256:") {
		fullName = fmt.Sprint(name, "@", tagOrDigest)
	} else {
		fullName = fmt.Sprint(name, ":", tagOrDigest)
	}
	if domain != "docker.io" {
		fullName = fmt.Sprint(domain, "/", fullName)
	}

	// note that Docker gives us a top-level index.json file with the mimetype
	// application/vnd.oci.image.index.v1+json which is handled correctly by K8s,
	// but results in a different image digest! Docker seems to calculate image
	// digests based off the manifest list file referenced in index.json, with
	// mimetype application/vnd.docker.distribution.manifest.list.v2+json.
	// in order to keep image ids identical to what users see in `docker image ls`,
	// follow the index.json and return the manifest list instead.
	if !strings.HasPrefix(tagOrDigest, "sha256:") {
		// get index.json, exporting image if we haven't yet
		r, err := openIndex(req.Context(), fullName)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		if r == nil {
			http.NotFound(w, req)
			return
		}
		content, err := io.ReadAll(r)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}

		// parse list of manifest files from index
		var index struct {
			// SchemaVersion int
			// MediaType string
			Manifests []struct {
				// MediaType string
				// Size int
				// Anotations map[string]string
				Digest string
			}
		}
		err = json.Unmarshal(content, &index)
		// TODO:for these errors, should we just log it and return the index content as-is?
		if err != nil {
			http.Error(w, fmt.Sprintf("%w while parsing: %v", err, string(content)), http.StatusInternalServerError)
			return
		}
		if len(index.Manifests) != 1 {
			http.Error(w, fmt.Sprintf("len(manifests) != 1 while parsing: %v", string(content)), http.StatusInternalServerError)
			return
		}

		// redirect to get the actual manifest
		http.Redirect(w, req, fmt.Sprintf("/v2/%s/manifests/%s?ns=%s", name, index.Manifests[0].Digest, domain), http.StatusFound)
		return
	}

	// at this point, we know we have a URL like image@sha256:shasum. all we need to do
	// is grab the manifest, likely a application/vnd.docker.distribution.manifest.list.v2+json,
	// possibly export the image if someone referenced this image directly via shasum
	// instead of via tag (which would go through the logic above), and then return
	// the contents to the client.
	// TODO: be smarter about content-type. grab the file mimetype by crawling the manifest...?
	fmt.Printf("%v\n", req.Header["Accept"])
	w.Header().Add("Content-Type", "application/vnd.oci.image.index.v1+json")
	shasum := strings.TrimPrefix(tagOrDigest, "sha256:")

	// get existing blob, or try to export image
	blob, err := openCachedBlobForSha256(shasum)
	if blob == nil && err == nil {
		// try to export image
		if _, err := openIndex(req.Context(), fullName); err != nil {
			blob, err = openCachedBlobForSha256(shasum)
		}
	}
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	if blob == nil {
		http.NotFound(w, req)
		return
	}
	if req.Method == "GET" {
		_, err = io.Copy(w, blob)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
	}
}

func main() {
	// CLI args
	port := flag.Int("port", 5000, "Port to listen on")
	flag.Parse()

	// set up our global docker client
	var err error
	docker, err = NewDockerClient()
	if err != nil {
		panic(err)
	}
	defer docker.Close()

	// the actual HTTP server
	// uses custom routing because image names may contain slashes / multiple path segments!
	// TODO: check HTTP method is GET or HEAD
	mux := NewRegexpServeMux()
	mux.HandleFunc("^/$", handleHelloWorld)
	mux.HandleFunc("^/v2/$", handleV2)
	mux.HandleFunc("^/v2/(?P<name>.+)/blobs/(?P<digest>[^/]+)$", handleBlobs)
	mux.HandleFunc("^/v2/(?P<name>.+)/manifests/(?P<tagOrDigest>[^/]+)$", handleManifests)
	log.Printf("Listening on :%d", *port)
	err = http.ListenAndServe(fmt.Sprintf(":%d", *port), LoggingMiddleware(mux))
	log.Fatal(err)
}
