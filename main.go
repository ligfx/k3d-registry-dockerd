package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

func readCachedIndex(imageId string, filename string) (io.Reader, error) {
	cachePath := cachedIndexFilename(imageId, filename)
	file, err := os.Open(cachePath)
	// if err == nil {
	// 	log.Print("Found ", cachePath)
	// }
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return file, err
}

func readCachedBlobForSha256(sha256 string) (io.Reader, error) {
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
			log.Printf("Wrote %s (%d bytes)", cachePath, bytesWritten)

			shasumbytes := sha256.Sum256(content)
			shasum := hex.EncodeToString(shasumbytes[:])
			cachePath = cachedBlobFilenameForSha256(shasum)
			bytesWritten, err = copyToFile(cachePath, bytes.NewReader(content))
			log.Printf("Wrote %s (%d bytes)", cachePath, bytesWritten)
		}
	}
}

func getManifest(ctx context.Context, fullName string) (io.Reader, error) {
	imageInfo, err := findImage(ctx, fullName)
	if err != nil {
		return nil, err
	}
	if imageInfo == nil {
		return nil, nil
	}

	reader, err := readCachedIndex(imageInfo.Id, "index.json")
	if err != nil {
		return nil, err
	}
	if reader != nil {
		return reader, nil
	}

	log.Printf("Exporting Docker image %s %s", fullName, imageInfo.Id)
	tarball, err := docker.ImageExport(ctx, imageInfo.Id)
	if err != nil {
		return nil, err
	}
	err = saveOciImageToCache(fullName, imageInfo.Id, tarball)
	if err != nil {
		return nil, err
	}
	return readCachedIndex(imageInfo.Id, "index.json")
}

func handleBlobs(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	// name := req.PathValue("name")
	digest := req.PathValue("digest")
	// req.ParseForm()
	// if len(req.Form["ns"]) != 1 {
	// 	// don't support acting as own repo
	// 	http.NotFound(w, req)
	// 	return
	// }
	// domain := req.Form["ns"][0]

	// TODO: check accept header?

	shasum := strings.TrimPrefix(digest, "sha256:")
	blob, err := readCachedBlobForSha256(shasum)
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
	req.ParseForm()
	if len(req.Form["ns"]) != 1 {
		// don't support acting as own repo
		http.NotFound(w, req)
		return
	}
	domain := req.Form["ns"][0]

	// handle manifests which are referenced by digest (just grab the blob)
	if strings.HasPrefix(tagOrDigest, "sha256:") {
		// TODO: be smarter about content-type. save the file mimetypes by crawling the manifests...?
		w.Header().Add("Content-Type", req.Header["Accept"][0])
		shasum := strings.TrimPrefix(tagOrDigest, "sha256:")
		blob, err := readCachedBlobForSha256(shasum)
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
		return
	}

	// TODO: check accept header?
	w.Header().Add("Content-Type", "application/vnd.oci.image.index.v1+json")

	// build the full image name, e.g. domain/namespace/image:tag
	fullName := fmt.Sprint(name, ":", tagOrDigest)
	if domain != "docker.io" {
		fullName = fmt.Sprint(domain, "/", fullName)
	}

	// try to get the manifest, then write it out
	// TODO: set content type
	manifest, err := getManifest(req.Context(), fullName)
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	if manifest == nil {
		http.NotFound(w, req)
		return
	}
	if req.Method == "GET" {
		_, err = io.Copy(w, manifest)
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
	blobsRegex := regexp.MustCompile("^/v2/(?P<name>.+)/blobs/(?P<digest>[^/]+)$")
	manifestsRegex := regexp.MustCompile("^/v2/(?P<name>.+)/manifests/(?P<tagOrDigest>[^/]+)$")
	http.HandleFunc("/", OneAtATimeMiddleware(LoggingMiddleware(func(w http.ResponseWriter, req *http.Request) {
		// TODO: check HTTP method is GET or HEAD

		// custom routing because image names may contain slashes / multiple path segments!
		var m []string
		m = blobsRegex.FindStringSubmatch(req.URL.Path)
		if m != nil {
			req.SetPathValue("name", m[1])
			req.SetPathValue("digest", m[2])
			handleBlobs(w, req)
			return
		}
		m = manifestsRegex.FindStringSubmatch(req.URL.Path)
		if m != nil {
			req.SetPathValue("name", m[1])
			req.SetPathValue("tagOrDigest", m[2])
			handleManifests(w, req)
			return
		}
		if req.URL.Path == "/v2" || req.URL.Path == "/v2/" {
			handleV2(w, req)
			return
		}
		if req.URL.Path == "/" {
			handleHelloWorld(w, req)
			return
		}
		http.NotFound(w, req)
		return
	})))
	log.Printf("Listening on :%d", *port)
	err = http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
	log.Fatal(err)
}
