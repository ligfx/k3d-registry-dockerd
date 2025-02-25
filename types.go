package main

import (
	"encoding/json"
	"fmt"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"os"
)

/*

type ocispec.Index struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType specifies the type of this document data structure e.g. `application/vnd.oci.image.index.v1+json`
	MediaType string `json:"mediaType,omitempty"`

	// ArtifactType specifies the IANA media type of artifact when the manifest is used for an artifact.
	ArtifactType string `json:"artifactType,omitempty"`

	// Manifests references platform specific manifests.
	Manifests []Descriptor `json:"manifests"`

	// Subject is an optional link from the image manifest to another manifest forming an association between the image manifest and the other manifest.
	Subject *Descriptor `json:"subject,omitempty"`

	// Annotations contains arbitrary metadata for the image index.
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ocispec.Manifest struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType specifies the type of this document data structure e.g. `application/vnd.oci.image.manifest.v1+json`
	MediaType string `json:"mediaType,omitempty"`

	// ArtifactType specifies the IANA media type of artifact when the manifest is used for an artifact.
	ArtifactType string `json:"artifactType,omitempty"`

	// Config references a configuration object for a container, by digest.
	// The referenced configuration object is a JSON blob that the runtime uses to set up the container.
	Config Descriptor `json:"config"`

	// Layers is an indexed list of layers referenced by the manifest.
	Layers []Descriptor `json:"layers"`

	// Subject is an optional link from the image manifest to another manifest forming an association between the image manifest and the other manifest.
	Subject *Descriptor `json:"subject,omitempty"`

	// Annotations contains arbitrary metadata for the image manifest.
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ocispec.Descriptor struct {
	// MediaType is the media type of the object this schema refers to.
	MediaType string `json:"mediaType"`

	// Digest is the digest of the targeted content.
	Digest digest.Digest `json:"digest"`

	// Size specifies the size in bytes of the blob.
	Size int64 `json:"size"`

	// URLs specifies a list of URLs from which this object MAY be downloaded
	URLs []string `json:"urls,omitempty"`

	// Annotations contains arbitrary metadata relating to the targeted content.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Data is an embedding of the targeted content. This is encoded as a base64
	// string when marshalled to JSON (automatically, by encoding/json). If
	// present, Data can be used directly to avoid fetching the targeted content.
	Data []byte `json:"data,omitempty"`

	// Platform describes the platform which the image in the manifest runs on.
	//
	// This should only be used when referring to a manifest.
	Platform *Platform `json:"platform,omitempty"`

	// ArtifactType is the IANA media type of this artifact.
	ArtifactType string `json:"artifactType,omitempty"`
}

*/

type MediaTyped struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType specifies the type of this document data structure e.g. `application/vnd.oci.image.index.v1+json`
	MediaType string `json:"mediaType"`
}

func ParseMediaTypedBytes(content []byte) (*MediaTyped, error) {
	var mt MediaTyped
	err := json.Unmarshal(content, &mt)
	if err != nil {
		return nil, fmt.Errorf("%w while parsing: %v", err, string(content))
	}
	return &mt, nil
}

func ParseMediaTypedFile(filename string) (*MediaTyped, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return ParseMediaTypedBytes(content)
}

func ParseIndexBytes(content []byte) (*ocispec.Index, error) {
	var index ocispec.Index
	err := json.Unmarshal(content, &index)
	if err != nil {
		return nil, fmt.Errorf("%w while parsing: %v", err, string(content))
	}
	return &index, nil
}

func ParseIndexFile(filename string) (*ocispec.Index, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return ParseIndexBytes(content)
}

func ParseManifestBytes(content []byte) (*ocispec.Manifest, error) {
	var manifest ocispec.Manifest
	err := json.Unmarshal(content, &manifest)
	if err != nil {
		return nil, fmt.Errorf("%w while parsing: %v", err, string(content))
	}
	return &manifest, nil
}

func ParseManifestFile(filename string) (*ocispec.Manifest, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return ParseManifestBytes(content)
}

func IsIndexType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.index.v1+json":
		return true
	case "application/vnd.docker.distribution.manifest.list.v2+json":
		return true
	default:
		return false
	}
}

func IsManifestType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.manifest.v1+json":
		return true
	case "application/vnd.docker.distribution.manifest.v2+json":
		return true
	default:
		return false
	}
}
