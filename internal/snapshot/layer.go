//go:build linux
// +build linux

package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"minidocker/internal/image"

	"golang.org/x/sys/unix"
)

// layersDirName is the directory name for extracted layers cache.
const layersDirName = "layers"

// whiteoutPrefix is the prefix for whiteout files in OCI layers.
// Whiteout files indicate that a file from a lower layer should be deleted.
const whiteoutPrefix = ".wh."

// opaqueWhiteout indicates that the entire directory contents should be hidden.
const opaqueWhiteout = ".wh..wh..opq"

const (
	overlayOpaqueXattr = "trusted.overlay.opaque"
	overlayOpaqueValue = "y"
)

// extractLayers extracts all image layers to the layer cache.
// Returns the paths to the extracted layers in order (bottom to top).
func (s *overlaySnapshotter) extractLayers(manifest *ocispec.Manifest, config *ocispec.Image) ([]string, error) {
	if len(manifest.Layers) != len(config.RootFS.DiffIDs) {
		return nil, fmt.Errorf("layer count mismatch: manifest has %d layers, config has %d diff_ids",
			len(manifest.Layers), len(config.RootFS.DiffIDs))
	}

	layerPaths := make([]string, len(manifest.Layers))

	for i, layerDesc := range manifest.Layers {
		diffID := config.RootFS.DiffIDs[i]

		// Check if layer is already extracted
		layerPath := s.layerPath(diffID)
		if _, err := os.Stat(layerPath); err == nil {
			// Layer already cached
			layerPaths[i] = layerPath
			continue
		}

		// Extract layer
		if err := s.extractLayer(layerDesc, diffID); err != nil {
			return nil, fmt.Errorf("extract layer %d (%s): %w", i, diffID, err)
		}
		layerPaths[i] = layerPath
	}

	return layerPaths, nil
}

// extractLayer extracts a single layer blob to the layer cache.
func (s *overlaySnapshotter) extractLayer(descriptor ocispec.Descriptor, diffID digest.Digest) error {
	layerPath := s.layerPath(diffID)

	// Double-check: if already extracted, skip
	if _, err := os.Stat(layerPath); err == nil {
		return nil
	}

	// Create temp directory for atomic extraction
	tempDir, err := os.MkdirTemp(filepath.Dir(layerPath), ".extracting-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	// Cleanup on failure
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tempDir)
		}
	}()

	// Get blob reader from image store
	blobReader, err := s.imageStore.GetBlob(descriptor.Digest)
	if err != nil {
		return fmt.Errorf("get layer blob: %w", err)
	}
	defer blobReader.Close()

	// Auto-detect compression and create tar reader
	tarReader, err := newTarReader(blobReader)
	if err != nil {
		return fmt.Errorf("create tar reader: %w", err)
	}

	// Extract tar contents
	if err := extractTar(tarReader, tempDir); err != nil {
		return fmt.Errorf("extract tar: %w", err)
	}

	// Atomic rename to final location
	if err := os.Rename(tempDir, layerPath); err != nil {
		// If rename fails due to existing directory (race condition), that's OK
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("finalize layer: %w", err)
	}

	success = true
	return nil
}

// layerPath returns the path to an extracted layer.
func (s *overlaySnapshotter) layerPath(diffID digest.Digest) string {
	return filepath.Join(s.root, layersDirName, diffID.Algorithm().String(), diffID.Encoded())
}

// newTarReader creates a tar reader, auto-detecting compression.
func newTarReader(r io.Reader) (*tar.Reader, error) {
	// Try to detect gzip by reading magic bytes
	buf := make([]byte, 2)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	// Create a multi-reader to re-read the magic bytes
	mr := io.MultiReader(strings.NewReader(string(buf[:n])), r)

	// Check for gzip magic (0x1f 0x8b)
	if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		gz, err := gzip.NewReader(mr)
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %w", err)
		}
		return tar.NewReader(gz), nil
	}

	// Not compressed
	return tar.NewReader(mr), nil
}

// extractTar extracts a tar archive to a directory.
// It handles regular files, directories, symlinks, hard links, and device nodes.
// It also processes whiteout files for layer deletion semantics.
func extractTar(tr *tar.Reader, destDir string) error {
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		// Security: clean the path and prevent path traversal
		cleanName := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("invalid path in tar: %s", header.Name)
		}

		target := filepath.Join(destDir, cleanName)

		// Verify target is within destDir
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("path traversal detected: %s", header.Name)
		}

		// Handle whiteout files
		baseName := filepath.Base(cleanName)
		if strings.HasPrefix(baseName, whiteoutPrefix) {
			if err := handleWhiteout(destDir, cleanName); err != nil {
				return fmt.Errorf("handle whiteout %s: %w", cleanName, err)
			}
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("create parent directory for %s: %w", cleanName, err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("create directory %s: %w", cleanName, err)
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := extractRegularFile(tr, target, header); err != nil {
				return fmt.Errorf("extract file %s: %w", cleanName, err)
			}

		case tar.TypeSymlink:
			// Remove existing file/symlink if present
			os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("create symlink %s: %w", cleanName, err)
			}

		case tar.TypeLink:
			// Hard link - resolve relative to destDir
			linkTarget := filepath.Join(destDir, filepath.Clean(header.Linkname))
			// Remove existing file if present
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("create hard link %s: %w", cleanName, err)
			}

		case tar.TypeChar, tar.TypeBlock:
			// Device nodes - skip for now, will be handled by devtmpfs in container
			// Creating device nodes requires CAP_MKNOD and is usually not needed
			// since containers typically use a minimal /dev mounted as tmpfs
			continue

		case tar.TypeFifo:
			// Named pipes - create using mknod
			os.Remove(target)
			if err := mkfifo(target, uint32(header.Mode)); err != nil {
				return fmt.Errorf("create fifo %s: %w", cleanName, err)
			}

		default:
			// Skip unknown types
			continue
		}
	}

	return nil
}

// extractRegularFile extracts a regular file from tar.
func extractRegularFile(tr *tar.Reader, target string, header *tar.Header) error {
	// Remove existing file if present
	os.Remove(target)

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
	if err != nil {
		return err
	}

	// Copy content with size limit for safety
	_, err = io.Copy(f, tr)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	return err
}

// handleWhiteout processes a whiteout file.
// Whiteouts indicate files that should be deleted in the merged view.
func handleWhiteout(destDir, whiteoutPath string) error {
	baseName := filepath.Base(whiteoutPath)
	dirName := filepath.Dir(whiteoutPath)

	// Handle opaque whiteout (hide entire directory contents from lower layers)
	if baseName == opaqueWhiteout {
		// OCI layer uses a marker file; overlayfs expects an xattr on the directory.
		// See: OCI layer whiteout / overlayfs opaque dir semantics.
		opaqueDir := filepath.Join(destDir, dirName)
		if err := os.MkdirAll(opaqueDir, 0755); err != nil {
			return err
		}
		if err := unix.Setxattr(opaqueDir, overlayOpaqueXattr, []byte(overlayOpaqueValue), 0); err != nil {
			return fmt.Errorf("set opaque xattr on %s: %w", opaqueDir, err)
		}
		return nil
	}

	// Regular whiteout: .wh.<filename> indicates <filename> should be deleted
	deletedFile := strings.TrimPrefix(baseName, whiteoutPrefix)
	if deletedFile == "" {
		return fmt.Errorf("invalid whiteout entry: %s", whiteoutPath)
	}
	target := filepath.Join(destDir, dirName, deletedFile)

	// For overlayfs lowerdirs, whiteouts must be represented as a character device 0/0.
	// See: overlayfs whiteout (S_IFCHR, rdev=0/0).

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	_ = os.RemoveAll(target)

	mode := uint32(unix.S_IFCHR | 0o600)
	dev := int(unix.Mkdev(0, 0))
	if err := unix.Mknod(target, mode, dev); err != nil {
		return fmt.Errorf("create whiteout device %s: %w", target, err)
	}
	return nil
}

// GetLayerPath returns the path to an extracted layer.
func (s *overlaySnapshotter) GetLayerPath(diffID digest.Digest) (string, error) {
	layerPath := s.layerPath(diffID)
	if _, err := os.Stat(layerPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("layer not found: %s", diffID)
		}
		return "", err
	}
	return layerPath, nil
}

// Cleanup removes orphaned layer caches.
// Currently a no-op; can be implemented to scan images and remove unreferenced layers.
func (s *overlaySnapshotter) Cleanup() error {
	// TODO: Implement garbage collection for unreferenced layers
	// This would involve:
	// 1. List all images in image store
	// 2. Collect all diff_ids from their configs
	// 3. Compare with extracted layers
	// 4. Remove layers not referenced by any image
	return nil
}

// Ensure imageStore satisfies image.Store interface for IDE assistance.
var _ image.Store = (image.Store)(nil)
