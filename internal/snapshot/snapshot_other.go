//go:build !linux
// +build !linux

package snapshot

import (
	"fmt"
	"runtime"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/internal/image"
)

// newOverlaySnapshotter returns an error on non-Linux platforms.
func newOverlaySnapshotter(rootDir string, imageStore image.Store) (*overlaySnapshotter, error) {
	return nil, fmt.Errorf("snapshotter is only supported on Linux (current: %s)", runtime.GOOS)
}

// overlaySnapshotter is a stub for non-Linux platforms.
type overlaySnapshotter struct{}

func (s *overlaySnapshotter) Prepare(containerID string, manifest *ocispec.Manifest, config *ocispec.Image) (string, error) {
	return "", fmt.Errorf("snapshotter is only supported on Linux (current: %s)", runtime.GOOS)
}

func (s *overlaySnapshotter) Remove(containerID string) error {
	return fmt.Errorf("snapshotter is only supported on Linux (current: %s)", runtime.GOOS)
}

func (s *overlaySnapshotter) GetLayerPath(diffID digest.Digest) (string, error) {
	return "", fmt.Errorf("snapshotter is only supported on Linux (current: %s)", runtime.GOOS)
}

func (s *overlaySnapshotter) Cleanup() error {
	return fmt.Errorf("snapshotter is only supported on Linux (current: %s)", runtime.GOOS)
}

func (s *overlaySnapshotter) GetSnapshotPath(containerID string) string {
	return ""
}
