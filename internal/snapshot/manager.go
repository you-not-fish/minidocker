//go:build linux
// +build linux

package snapshot

import (
	"fmt"
	"os"
	"path/filepath"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/internal/image"
)

// overlaySnapshotter implements the Snapshotter interface using overlayfs.
type overlaySnapshotter struct {
	root       string      // snapshots root directory (e.g., /var/lib/minidocker/snapshots)
	imageStore image.Store // image store for blob access
}

// newOverlaySnapshotter creates a new overlay snapshotter.
func newOverlaySnapshotter(rootDir string, imageStore image.Store) (*overlaySnapshotter, error) {
	snapshotRoot := filepath.Join(rootDir, DefaultSnapshotsDir)

	// Create directory structure
	layersDir := filepath.Join(snapshotRoot, layersDirName, "sha256")
	containersDir := filepath.Join(snapshotRoot, containersDirName)

	if err := os.MkdirAll(layersDir, 0755); err != nil {
		return nil, fmt.Errorf("create layers directory: %w", err)
	}
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		return nil, fmt.Errorf("create containers directory: %w", err)
	}

	return &overlaySnapshotter{
		root:       snapshotRoot,
		imageStore: imageStore,
	}, nil
}

// Prepare creates a writable snapshot for a container from an image.
// It extracts layers if needed, creates upper/work dirs, and mounts overlay.
// Returns the path to the mounted rootfs.
func (s *overlaySnapshotter) Prepare(containerID string, manifest *ocispec.Manifest, config *ocispec.Image) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("manifest is nil")
	}
	if config == nil {
		return "", fmt.Errorf("config is nil")
	}

	// Extract all layers to cache (if not already extracted)
	layerPaths, err := s.extractLayers(manifest, config)
	if err != nil {
		return "", fmt.Errorf("extract layers: %w", err)
	}

	// Create container snapshot directories
	snapshotDir := s.containerSnapshotDir(containerID)
	upperDir := s.containerUpperDir(containerID)
	workDir := s.containerWorkDir(containerID)

	// Clean up any existing snapshot (in case of re-prepare)
	if err := s.Remove(containerID); err != nil {
		// Ignore errors during cleanup
	}

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}

	if err := os.MkdirAll(upperDir, 0755); err != nil {
		return "", fmt.Errorf("create upper directory: %w", err)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("create work directory: %w", err)
	}

	// Mount point is inside the snapshot directory
	// This will be the container's rootfs
	mountPoint := filepath.Join(snapshotDir, "rootfs")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", fmt.Errorf("create mount point: %w", err)
	}

	// Mount overlay
	if err := mountOverlay(layerPaths, upperDir, workDir, mountPoint); err != nil {
		// Cleanup on failure
		os.RemoveAll(snapshotDir)
		return "", fmt.Errorf("mount overlay: %w", err)
	}

	return mountPoint, nil
}

// Remove unmounts and removes a container's snapshot.
// It cleans up the upper/work directories but preserves cached layers.
func (s *overlaySnapshotter) Remove(containerID string) error {
	snapshotDir := s.containerSnapshotDir(containerID)

	// Check if snapshot exists
	if _, err := os.Stat(snapshotDir); os.IsNotExist(err) {
		return nil // Nothing to remove
	}

	// Unmount overlay first
	mountPoint := filepath.Join(snapshotDir, "rootfs")
	if err := unmountOverlay(mountPoint); err != nil {
		// Log but continue with cleanup
		fmt.Fprintf(os.Stderr, "warning: failed to unmount overlay for %s: %v\n", containerID, err)
	}

	// Remove the container's snapshot directory (upper, work, rootfs mount point)
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("remove snapshot directory: %w", err)
	}

	return nil
}

// GetSnapshotPath returns the snapshot directory path for a container.
// This is used to track the snapshot path in container state.
func (s *overlaySnapshotter) GetSnapshotPath(containerID string) string {
	return s.containerSnapshotDir(containerID)
}

// Ensure overlaySnapshotter implements Snapshotter.
var _ Snapshotter = (*overlaySnapshotter)(nil)
