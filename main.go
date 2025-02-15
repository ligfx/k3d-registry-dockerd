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
	"io"
	"log"
	"net/http"
	"net/url"
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

func findImage(ctx context.Context, imageName, imageTagOrDigest string) (*DockerImageInfo, error) {
	var fullName string
	if strings.HasPrefix(imageTagOrDigest, "sha256:") {
		fullName = fmt.Sprint(imageName, "@", imageTagOrDigest)
	} else {
		fullName = fmt.Sprint(imageName, ":", imageTagOrDigest)
	}
	if strings.HasPrefix(fullName, "docker.io/") {
		// TODO: consider passing domain separately
		fullName = strings.TrimPrefix(fullName, "docker.io/")
	}

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

func cachedImageDirectory(imageName string) string {
	safeImageName := url.QueryEscape(imageName)
	if safeImageName == "" || safeImageName == "." || safeImageName == ".." {
		panic(safeImageName)
	}
	return fmt.Sprint(CACHE_DIRECTORY, "/", safeImageName)
}

func cachedIndexFilename(imageName, imageTagOrDigest string) string {
	safeImageTagOrDigest := url.QueryEscape(imageTagOrDigest)
	if safeImageTagOrDigest == "" || safeImageTagOrDigest == "." || safeImageTagOrDigest == ".." {
		panic(safeImageTagOrDigest)
	}
	return fmt.Sprint(cachedImageDirectory(imageName), "/indexes/", safeImageTagOrDigest, "/index.json")
}

func cachedBlobFilenameForSha256(imageName, sha256 string) string {
	return fmt.Sprint(cachedImageDirectory(imageName), "/blobs/sha256/", sha256)
}

func openCachedBlobForSha256(imageName, sha256 string) (io.Reader, error) {
	cachePath := cachedBlobFilenameForSha256(imageName, sha256)
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
	bytesWritten, err := io.Copy(f, reader)
	if err != nil && errors.Is(err, io.ErrUnexpectedEOF) {
		// happens when reading an HTTP request body
		err = nil
	}
	return bytesWritten, err
}

func fileExists(filename string) (bool, error) {
	_, err := os.Stat(filename)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func saveOciImageToCache(imageName string, imageTagOrDigest string, tarball *tar.Reader) error {
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

		if strings.HasPrefix(header.Name, "blobs/") {
			// blobs get written directly
			cachePath := fmt.Sprint(cachedImageDirectory(imageName), "/", header.Name)
			exists, err := fileExists(cachePath)
			if err != nil {
				return err
			}
			if exists {
				log.Printf("Skipping %s %s", imageName, header.Name)
			} else {
				bytesWritten, err := copyToFile(cachePath, tarball)
				if err != nil {
					return err
				}
				log.Printf("Wrote %s %s (%d bytes)", imageName, header.Name, bytesWritten)
			}
		} else if header.Name == "index.json" && !strings.HasPrefix(imageTagOrDigest, "sha256:") {
			// index files get written to a directory depending on the image tag
			// if the image is being referenced by digest, though, we don't care
			content, err := io.ReadAll(tarball)
			if err != nil {
				return err
			}

			cachePath := cachedIndexFilename(imageName, imageTagOrDigest)
			bytesWritten, err := copyToFile(cachePath, bytes.NewReader(content))
			if err != nil {
				return err
			}
			log.Printf("Wrote %s/%s %s (%d bytes)", imageName, imageTagOrDigest, header.Name, bytesWritten)
		} else {
			log.Printf("Ignoring %s/%s %s", imageName, imageTagOrDigest, header.Name)
		}
	}
}

var imageMutexPool KeyedMutexPool

func ensureImageInCache(ctx context.Context, imageName, imageTagOrDigest string) (bool, error) {
	found, err := imageMutexPool.Do(imageName, func() (any, error) {
		// check if we have either the index for a tag, or the blob for a digest
		var cachePath string
		if strings.HasPrefix(imageTagOrDigest, "sha256:") {
			cachePath = cachedBlobFilenameForSha256(imageName, strings.TrimPrefix(imageTagOrDigest, "sha256:"))
		} else {
			cachePath = cachedIndexFilename(imageName, imageTagOrDigest)
		}
		_, err := os.Stat(cachePath)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}

		// otherwise, find and export the image
		imageInfo, err := findImage(ctx, imageName, imageTagOrDigest)
		if err != nil {
			return false, err
		}
		if imageInfo == nil {
			return false, nil
		}

		if strings.HasPrefix(imageTagOrDigest, "sha256:") {
			log.Printf("Exporting Docker image %s@%s", imageName, imageTagOrDigest)
		} else {
			log.Printf("Exporting Docker image %s:%s@%s", imageName, imageTagOrDigest, imageInfo.Id)
		}
		err = docker.ImageExport(ctx, imageInfo.Id, func(tarball *tar.Reader) error {
			return saveOciImageToCache(imageName, imageTagOrDigest, tarball)
		})
		if err != nil {
			return false, err
		}
		return true, nil
	})
	return found.(bool), err
}

func handleBlobUpload(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	name := req.PathValue("name")
	digest := req.URL.Query().Get("digest")

	// if no digest, this is the two-step upload process. respond with the same
	// URL, and expect a PUT request next.
	if digest == "" {
		if req.Method != "POST" {
			http.Error(w, "", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads", name))
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// if we have a digest, it's either a one-step upload with POST or the
	// second part of a two-step upload with PUT.
	// TODO: handle chunked uploads with PATCH?
	if !(req.Method == "POST" || req.Method == "PUT") {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(digest, "sha256:") {
		http.Error(w, fmt.Sprintf("don't know how to handle digest %q", digest), http.StatusInternalServerError)
		return
	}
	shasum := strings.TrimPrefix(digest, "sha256:")
	cachePath := cachedBlobFilenameForSha256(name, shasum)
	bytesWritten, err := copyToFile(cachePath, req.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("error writing %q: %s", cachePath, err), http.StatusInternalServerError)
		return
	} else {
		log.Printf("Wrote %s blobs/sha256/%s (%d bytes)", name, shasum, bytesWritten)
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, digest))
	// docker push, as used by Tilt (and maybe other tools), requires this header
	// TODO: actually calculate and check our own digest?
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

func handleBlobs(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	name := req.PathValue("name")
	digest := req.PathValue("digest")
	domain := req.URL.Query().Get("ns")
	if domain != "" {
		name = fmt.Sprint(domain, "/", name)
	}

	// TODO: check accept header?

	shasum := strings.TrimPrefix(digest, "sha256:")
	blob, err := openCachedBlobForSha256(name, shasum)
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

func handleManifestUpload(w http.ResponseWriter, req *http.Request) {
	// get the HTTP arguments
	name := req.PathValue("name")
	tagOrDigest := req.PathValue("tagOrDigest")

	// read the manifest to upload
	content, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}

	// calculate digest, make sure it matches tagOrDigest if necessary
	shasumbytes := sha256.Sum256(content)
	shasum := hex.EncodeToString(shasumbytes[:])
	if strings.HasPrefix(tagOrDigest, "sha256:") && shasum != strings.TrimPrefix(tagOrDigest, "sha256:") {
		http.Error(w, fmt.Sprint("Mismatched calculated digest %s", shasum), http.StatusBadRequest)
		return
	}

	// write manifest as a blob
	// TODO: what if it already exists?
	cachePath := cachedBlobFilenameForSha256(name, shasum)
	bytesWritten, err := copyToFile(cachePath, bytes.NewReader(content))
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	log.Printf("Wrote %s blobs/sha256/%s (%d bytes)", name, shasum, bytesWritten)

	// write index, if necessary
	if !strings.HasPrefix(tagOrDigest, "sha256:") {
		indexPath := cachedIndexFilename(name, tagOrDigest)
		// TODO: write a proper index file
		indexContent := fmt.Sprintf(`{"manifests":[{"digest":"sha256:%s"}]}`, shasum)
		bytesWritten, err = copyToFile(indexPath, strings.NewReader(indexContent))
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		log.Printf("Wrote %s/%s index.json (%d bytes)", name, tagOrDigest, bytesWritten)
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", name, tagOrDigest))
	// docker push, as used by Tilt (and maybe other tools), requires this header
	w.Header().Set("Docker-Content-Digest", fmt.Sprintf("sha256:%s", shasum))
	w.WriteHeader(http.StatusCreated)
}

func handleManifests(w http.ResponseWriter, req *http.Request) {
	// handle uploads
	if req.Method == "PUT" {
		handleManifestUpload(w, req)
		return
	}

	// get the HTTP arguments
	name := req.PathValue("name")
	tagOrDigest := req.PathValue("tagOrDigest")
	domain := req.URL.Query().Get("ns")
	if domain != "" {
		name = fmt.Sprint(domain, "/", name)
	}

	// export image if we haven't yet
	if domain != "" {
		found, err := ensureImageInCache(req.Context(), name, tagOrDigest)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, req)
			return
		}
	}

	// note that Docker gives us a top-level index.json file with the mimetype
	// application/vnd.oci.image.index.v1+json which is handled correctly by K8s,
	// but results in a different image digest! Docker seems to calculate image
	// digests based off the manifest list file referenced in index.json, with
	// mimetype application/vnd.docker.distribution.manifest.list.v2+json.
	// in order to keep image ids identical to what users see in `docker image ls`,
	// follow the index.json and return the manifest list instead.
	if !strings.HasPrefix(tagOrDigest, "sha256:") {
		content, err := os.ReadFile(cachedIndexFilename(name, tagOrDigest))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, req)
			} else {
				http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			}
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

		// we now have the actual manifest digest, so fall through to the logic to
		// grab and return it.
		tagOrDigest = index.Manifests[0].Digest
	}

	// at this point, we know we have a URL like image@sha256:shasum. all we need to do
	// is grab the manifest, likely a application/vnd.docker.distribution.manifest.list.v2+json,
	// and then return the contents to the client.
	shasum := strings.TrimPrefix(tagOrDigest, "sha256:")
	blob, err := openCachedBlobForSha256(name, shasum)
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	if blob == nil {
		http.NotFound(w, req)
		return
	}

	// get the file mimetype from the mediaType json field. all OCI manifest
	// types should have this.
	content, err := io.ReadAll(blob)
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	var manifest struct {
		// all OCI manifest files have this field
		MediaType string
	}
	err = json.Unmarshal(content, &manifest)
	if err != nil {
		// k8s seems to require a valid content-type for manifest files. if it
		// doesn't get one, containers will be stuck in "creating" forever.
		http.Error(w, fmt.Sprintf("%w while parsing, not setting Content-Type for: %v",
			err, string(content)), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", manifest.MediaType)

	if req.Method == "GET" {
		_, err = w.Write(content)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
	}
}

func main() {
	// Config taken from CLI args or environment variables
	defaultAddr := ":5000"
	environAddrName := "REGISTRY_HTTP_ADDR"
	addr := flag.String("addr", "", fmt.Sprintf("Address to listen on (default %q, or value of environment variable %s)", defaultAddr, environAddrName))
	flag.Parse()

	environAddr := os.Getenv(environAddrName)

	if *addr != "" {
		if environAddr != "" {
			log.Printf("Ignoring environment variable %s=%q", environAddrName, environAddr)
		}
		log.Printf("Using address specified in command line arguments: %q", *addr)
	} else if environAddr != "" {
		log.Printf("Using address specified in environment variable %s=%q", environAddrName, environAddr)
		*addr = environAddr
	} else {
		log.Printf("Using default address: %q", defaultAddr)
		*addr = defaultAddr
	}

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
	mux.HandleFunc("^/v2/(?P<name>.+)/blobs/uploads", handleBlobUpload)
	mux.HandleFunc("^/v2/(?P<name>.+)/blobs/(?P<digest>[^/]+)$", handleBlobs)
	mux.HandleFunc("^/v2/(?P<name>.+)/manifests/(?P<tagOrDigest>[^/]+)$", handleManifests)
	log.Printf("Listening on %s", *addr)
	err = http.ListenAndServe(*addr, LoggingMiddleware(mux))
	log.Fatal(err)
}
