// Package image provides OCI image storage and management.
// It implements a content-addressable store following the OCI Image Layout specification.
package image

import (
	"io"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image represents a container image in the store.
type Image struct {
	// ID is the content-addressable ID (digest of manifest).
	ID digest.Digest `json:"id"`

	// RepoTags contains repository tags (e.g., ["alpine:latest", "alpine:3.18"]).
	RepoTags []string `json:"repoTags,omitempty"`

	// Size is the total size of all layers in bytes.
	Size int64 `json:"size"`

	// Created is the creation time from the config.
	Created time.Time `json:"created,omitempty"`

	// Architecture is the CPU architecture from the config (e.g., "amd64").
	Architecture string `json:"architecture"`

	// OS is the operating system from the config (e.g., "linux").
	OS string `json:"os"`

	// Manifest is the parsed manifest (includes config/layer descriptors).
	// Not serialized to JSON.
	Manifest *ocispec.Manifest `json:"-"`

	// Config is the parsed config (includes runtime config, rootfs).
	// Not serialized to JSON.
	Config *ocispec.Image `json:"-"`
}

// Store manages image storage operations.
type Store interface {
	// Import imports an OCI tar archive and returns the image.
	// If ref is provided, it tags the image with that reference.
	Import(tarPath string, ref string) (*Image, error)

	// List returns all images in the store.
	List() ([]*Image, error)

	// Get retrieves an image by reference (name:tag or digest).
	Get(ref string) (*Image, error)

	// Delete removes an image by reference.
	// Returns ErrImageInUse if the image is referenced by containers.
	Delete(ref string) error

	// Tag adds a tag to an existing image.
	Tag(source, target string) error

	// GetBlob returns a reader for the blob content.
	GetBlob(dgst digest.Digest) (io.ReadCloser, error)

	// PutBlob writes content and returns its digest and size.
	PutBlob(r io.Reader) (digest.Digest, int64, error)

	// HasBlob checks if a blob exists.
	HasBlob(dgst digest.Digest) bool

	// GetManifest returns parsed manifest for an image.
	GetManifest(dgst digest.Digest) (*ocispec.Manifest, error)

	// GetConfig returns parsed config for an image.
	GetConfig(dgst digest.Digest) (*ocispec.Image, error)
}

// Repositories holds the mapping from name:tag to manifest digest.
type Repositories struct {
	// Refs maps reference strings to manifest digests.
	// e.g., {"alpine:latest": "sha256:abc...", "alpine:3.18": "sha256:abc..."}
	Refs map[string]digest.Digest `json:"refs"`
}

// NewRepositories creates an empty Repositories struct.
func NewRepositories() *Repositories {
	return &Repositories{
		Refs: make(map[string]digest.Digest),
	}
}

// OCI Image Layout constants.
const (
	// ImageLayoutVersion is the current OCI image layout version.
	ImageLayoutVersion = "1.0.0"

	// ImageLayoutFile is the filename for the layout version marker.
	ImageLayoutFile = "oci-layout"

	// ImageIndexFile is the filename for the image index.
	ImageIndexFile = "index.json"

	// BlobsDir is the directory name for blobs.
	BlobsDir = "blobs"

	// RepositoriesFile is the filename for the repositories mapping.
	// This is a minidocker extension, not part of OCI spec.
	RepositoriesFile = "repositories.json"
)

// ImageLayout represents the oci-layout file content.
type ImageLayout struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}
