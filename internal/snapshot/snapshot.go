// Package snapshot implements container rootfs snapshots using overlayfs.
// It extracts OCI image layers and mounts them as container root filesystems.
package snapshot

import (
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/internal/image"
)

// DefaultSnapshotsDir is the default directory name for snapshots.
const DefaultSnapshotsDir = "snapshots"

// Snapshotter manages container root filesystems from OCI images.
type Snapshotter interface {
	// Prepare creates a writable snapshot for a container from an image.
	// It extracts layers if needed, creates upper/work dirs, and mounts overlay.
	// Returns the path to the mounted rootfs.
	Prepare(containerID string, manifest *ocispec.Manifest, config *ocispec.Image) (rootfsPath string, err error)

	// Remove unmounts and removes a container's snapshot.
	// It cleans up the upper/work directories but preserves cached layers.
	Remove(containerID string) error

	// GetLayerPath returns the path to an extracted layer (for inspection).
	GetLayerPath(diffID digest.Digest) (string, error)

	// Cleanup removes orphaned layer caches not referenced by any image.
	// This is a maintenance operation and can be called periodically.
	Cleanup() error
}

// SnapshotInfo contains metadata about a container's snapshot.
type SnapshotInfo struct {
	ContainerID string          `json:"containerId"`
	ImageID     digest.Digest   `json:"imageId"`
	RootfsPath  string          `json:"rootfsPath"`
	UpperPath   string          `json:"upperPath"`
	WorkPath    string          `json:"workPath"`
	LowerDirs   []string        `json:"lowerDirs"`
	Mounted     bool            `json:"mounted"`
}

// NewSnapshotter creates a new overlay snapshotter.
// rootDir is the minidocker root directory (e.g., /var/lib/minidocker).
// imageStore is used to access image blobs for layer extraction.
func NewSnapshotter(rootDir string, imageStore image.Store) (Snapshotter, error) {
	return newOverlaySnapshotter(rootDir, imageStore)
}
