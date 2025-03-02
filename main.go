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
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func handleHelloWorld(w http.ResponseWriter, req *http.Request) {
	fmt.Fprint(w, "Hello, world!\n")
}

func handleV2(w http.ResponseWriter, req *http.Request) {
	// path has to return 2xx but doesn't have to have content
}

func findAndExportImage(ctx context.Context, imageName, imageTagOrDigest string) (bool, error) {
	// turn image name and tagOrDigest into a single string that's recognizable
	// as an image by Docker.
	var fullName string
	if strings.HasPrefix(imageTagOrDigest, "sha256:") {
		fullName = fmt.Sprint(imageName, "@", imageTagOrDigest)
	} else {
		fullName = fmt.Sprint(imageName, ":", imageTagOrDigest)
	}
	if strings.HasPrefix(fullName, "docker.io/") {
		// remove "docker.io" as an image's domain since that's assumed by Docker
		// if no domain is passed.
		// TODO: consider passing domain separately
		fullName = strings.TrimPrefix(fullName, "docker.io/")
		if strings.HasPrefix(fullName, "library/") {
			// remove "library" as an image's namespace if pulling from docker.io,
			// so that non-namespaced images like alpine:latest or busybox:latest work.
			fullName = strings.TrimPrefix(fullName, "library/")
		}
	}

	// find or pull image.
	image, err := DockerImageInspect(ctx, fullName)
	if err != nil {
		return false, err
	}
	if image == nil {
		log.Printf("Pulling Docker image %s", fullName)
		found, err := DockerImagePull(ctx, fullName, func(statusMessage string) {
			log.Println(statusMessage)
		})
		if err != nil {
			return false, err
		}
		if !found {
			log.Printf("Couldn't find Docker image %s", fullName)
			return false, nil
		}
	}

	// export it into our local cache.
	log.Printf("Exporting Docker image %s", fullName)
	err = DockerImageExport(ctx, fullName, func(tarball *tar.Reader) error {
		return saveOciImageToCache(imageName, imageTagOrDigest, tarball)
	})
	if err != nil {
		return false, err
	}

	// check that the export is valid. there are certain scenarios where Docker
	// will export images that don't have the correct ID, are missing blobs, etc.

	// Scenario 1. Images referenced by digest aren't exported with the correct ID
	// when using a non-containerd image store backend.
	// See https://github.com/ligfx/k3d-registry-dockerd/issues/14
	if strings.HasPrefix(imageTagOrDigest, "sha256:") {
		shasum := strings.TrimPrefix(imageTagOrDigest, "sha256:")
		cachePath := cachedBlobFilenameForSha256(imageName, shasum)
		exists, err := fileExists(cachePath)
		if err != nil {
			return false, err
		}
		if !exists {
			log.Printf(
				"Error: exported Docker image %s was missing blob %s. This is known to happen when referencing"+
					" images directly by SHA256-digest. To export this image correctly, switch to Docker's containerd"+
					" image store: https://docs.docker.com/desktop/features/containerd/",
				fullName, imageTagOrDigest)
			// report that the image was not found, so that we return HTTP 404. this
			// lets k8s know to try another registry, rather than looping forever
			// retrying an HTTP 500.
			return false, nil
		}
	}

	// Scenario 2. Images which have layers in common with other images may be
	// exported without any layer blobs when the containerd image store is being
	// used.
	// See https://github.com/ligfx/k3d-registry-dockerd/issues/13
	// and https://github.com/moby/moby/issues/49473
	var manifestDigest string
	if strings.HasPrefix(imageTagOrDigest, "sha256:") {
		manifestDigest = strings.TrimPrefix(imageTagOrDigest, "sha256:")
	} else {
		index, err := ParseIndexFile(cachedIndexFilename(imageName, imageTagOrDigest))
		if err != nil {
			return false, err
		}
		if len(index.Manifests) != 1 {
			// TODO: ???
			return false, fmt.Errorf("len(manifests) != 1 while parsing: %v", index)
		}
		manifestDigest = index.Manifests[0].Digest.Encoded()
	}
	blobsExist, err := checkManifestAndReferencedBlobsExist(imageName, manifestDigest)
	if err != nil {
		return false, err
	}
	if !blobsExist {
		// try to fix the image by going through Buildkit, which will download all of the
		// blobs and fix future exports.
		log.Printf("Attempting to fix %s using BuildKit", fullName)
		err = BuildkitForceDockerPull(ctx, fullName)
		if err != nil {
			// if we get an error, just print and move on
			log.Printf("Error while attempting to use BuildKit: %s", err)
		} else {
			// if BuildKit was successful, re-export image and check it again
			log.Printf("Re-exporting %s", fullName)
			err = DockerImageExport(ctx, fullName, func(tarball *tar.Reader) error {
				return saveOciImageToCache(imageName, imageTagOrDigest, tarball)
			})
			if err != nil {
				return false, err
			}
			blobsExist, err = checkManifestAndReferencedBlobsExist(imageName, manifestDigest)
			if err != nil {
				return false, err
			}
		}
	}
	if !blobsExist {
		log.Printf(
			"Error: exported Docker image %s was missing referenced blobs. This is known to happen when"+
				" using Docker's containerd image store and pulling images that share layers. See"+
				" https://github.com/ligfx/k3d-registry-dockerd/issues/13 and https://github.com/moby/moby/issues/49473",
			fullName)
		// remove the index so that we don't return HTTP 200 in the future.
		if !strings.HasPrefix(imageTagOrDigest, "sha256:") {
			err := os.Remove(cachedIndexFilename(imageName, imageTagOrDigest))
			if err != nil {
				return false, err
			}
			log.Printf("Removing %s index.json", fullName)
		}
		// report that the image was not found, so that we return HTTP 404. this
		// lets k8s know to try another registry, rather than looping forever
		// retrying an HTTP 500.
		return false, nil
	}

	return true, nil
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

func checkManifestAndReferencedBlobsExist(imageName, shasum string) (bool, error) {
	// This function exists because Docker can sometimes return images with manifests
	// but no blobs! See https://github.com/ligfx/k3d-registry-dockerd/issues/13
	// and https://github.com/moby/moby/issues/49473

	// read and parse as an OCI manifest or index
	cachePath := cachedBlobFilenameForSha256(imageName, shasum)
	mt, err := ParseMediaTypedFile(cachePath)
	if err != nil {
		return false, err
	}

	// for indexes, we want to make sure that for all referenced manifests that exist,
	// all of their blobs exist
	if IsIndexType(mt.MediaType) {
		index, err := ParseIndexFile(cachePath)
		if err != nil {
			return false, err
		}
		allExistingManifestsOkay := true
		for _, m := range index.Manifests {
			exists, err := fileExists(cachedBlobFilenameForSha256(imageName, m.Digest.Encoded()))
			if err != nil {
				return false, err
			}
			if exists {
				ok, err := checkManifestAndReferencedBlobsExist(imageName, m.Digest.Encoded())
				if err != nil {
					return false, err
				}
				allExistingManifestsOkay = allExistingManifestsOkay && ok
			}
		}
		return allExistingManifestsOkay, nil
	}

	// for manifests, we want to make sure that all referenced blobs exist.
	if IsManifestType(mt.MediaType) {
		manifest, err := ParseManifestFile(cachePath)
		if err != nil {
			return false, err
		}

		missingDigests := []string{}

		// check config blob
		blobPath := cachedBlobFilenameForSha256(imageName, manifest.Config.Digest.Encoded())
		exists, err := fileExists(blobPath)
		if err != nil {
			return false, fmt.Errorf("error checking %q: %w", blobPath, err)
		}
		if !exists && len(manifest.Config.Data) == 0 {
			missingDigests = append(missingDigests, manifest.Config.Digest.Encoded())
		}

		// check layer blobs
		for _, layer := range manifest.Layers {
			blobPath := cachedBlobFilenameForSha256(imageName, layer.Digest.Encoded())
			ok, err := fileExists(blobPath)
			if err != nil {
				return false, fmt.Errorf("error checking %q: %w", blobPath, err)
			}
			if !ok && len(layer.Data) == 0 {
				missingDigests = append(missingDigests, layer.Digest.Encoded())
			}
		}

		if len(missingDigests) > 0 {
			log.Printf("Manifest %s@sha256:%s missing blobs: %v", imageName, shasum, missingDigests)
			return false, nil
		}
		return true, nil
	}

	// for any other file type, if it exists assume we're okay
	log.Printf("checkManifestAndReferencedBlobsExist %s %s unknown media type %s", imageName, shasum, mt.MediaType)
	return true, nil
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
		exists, err := fileExists(cachePath)
		if err != nil {
			return false, nil
		}
		if exists {
			return true, nil
		}

		// otherwise, find and export the image
		return findAndExportImage(ctx, imageName, imageTagOrDigest)
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
		index, err := ParseIndexFile(cachedIndexFilename(name, tagOrDigest))
		// TODO: for these errors, should we just log it and return the index content as-is?
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, req)
				return
			}
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		if len(index.Manifests) != 1 {
			http.Error(w, fmt.Sprintf("len(manifests) != 1 while parsing: %v", index), http.StatusInternalServerError)
			return
		}

		// we now have the actual manifest digest, so fall through to the logic to
		// grab and return it.
		tagOrDigest = index.Manifests[0].Digest.String()
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
	mt, err := ParseMediaTypedBytes(content)
	if err != nil {
		// k8s seems to require a valid content-type for manifest files. if it
		// doesn't get one, containers will be stuck in "creating" forever.
		http.Error(w, fmt.Sprintf("%w while parsing, not setting Content-Type for: %v",
			err, string(content)), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", mt.MediaType)

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

	// test docker client
	ctx := context.Background()
	info, err := DockerGetInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Connected to Docker API v%s at %s ServerVersion=%s ServerOSType=%s ServerArchitecture=%s",
		info.ApiVersion,
		info.DaemonHost,
		info.ServerVersion,
		info.ServerOSType,
		info.ServerArchitecture)

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
