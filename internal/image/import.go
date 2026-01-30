//go:build linux
// +build linux

package image

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// importOCITar imports an OCI tar archive into the store.
func importOCITar(s *imageStore, tarPath string, ref string) (*Image, error) {
	// Open the tar file
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, fmt.Errorf("open tar file: %w", err)
	}
	defer f.Close()

	// Detect compression and create appropriate reader
	tr, err := newTarReader(f)
	if err != nil {
		return nil, fmt.Errorf("create tar reader: %w", err)
	}

	// Track extracted content for verification
	var (
		hasLayout   bool
		index       *ocispec.Index
		blobs       = make(map[string]string) // tar path -> digest
		blobSizes   = make(map[string]int64)  // digest -> size
	)

	// First pass: extract all content
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		// Normalize path (remove leading ./)
		name := strings.TrimPrefix(header.Name, "./")

		switch {
		case name == ImageLayoutFile:
			// Validate oci-layout
			var layout ImageLayout
			if err := json.NewDecoder(tr).Decode(&layout); err != nil {
				return nil, fmt.Errorf("decode oci-layout: %w", err)
			}
			if layout.ImageLayoutVersion != ImageLayoutVersion {
				return nil, fmt.Errorf("unsupported OCI layout version: %s (expected %s)",
					layout.ImageLayoutVersion, ImageLayoutVersion)
			}
			hasLayout = true

		case name == ImageIndexFile:
			// Parse index.json
			index = &ocispec.Index{}
			if err := json.NewDecoder(tr).Decode(index); err != nil {
				return nil, fmt.Errorf("decode index.json: %w", err)
			}

		case strings.HasPrefix(name, BlobsDir+"/"):
			// Extract blob
			dgst, size, err := s.extractBlob(tr, name)
			if err != nil {
				return nil, fmt.Errorf("extract blob %s: %w", name, err)
			}
			blobs[name] = dgst.String()
			blobSizes[dgst.String()] = size
		}
	}

	// Validate
	if !hasLayout {
		return nil, fmt.Errorf("invalid OCI archive: missing %s", ImageLayoutFile)
	}
	if index == nil {
		return nil, fmt.Errorf("invalid OCI archive: missing %s", ImageIndexFile)
	}
	if len(index.Manifests) == 0 {
		return nil, fmt.Errorf("invalid OCI archive: no manifests in index")
	}

	// Select a manifest to import.
	// Prefer linux/amd64 when multiple manifests exist (OCI index can be multi-platform).
	manifestDesc, err := selectManifestDescriptor(index)
	if err != nil {
		return nil, err
	}

	// If annotation specifies a ref name, use it as fallback
	if ref == "" {
		if annotRef, ok := manifestDesc.Annotations[ocispec.AnnotationRefName]; ok {
			ref = annotRef
		}
	}

	// Load and validate manifest
	manifest, err := s.GetManifest(manifestDesc.Digest)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	// Verify all required blobs exist
	if !s.HasBlob(manifest.Config.Digest) {
		return nil, fmt.Errorf("missing config blob: %s", manifest.Config.Digest)
	}
	for _, layer := range manifest.Layers {
		if !s.HasBlob(layer.Digest) {
			return nil, fmt.Errorf("missing layer blob: %s", layer.Digest)
		}
	}

	// Add manifest to index (if not already present)
	existingIndex, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	manifestExists := false
	for _, desc := range existingIndex.Manifests {
		if desc.Digest == manifestDesc.Digest {
			manifestExists = true
			break
		}
	}
	if !manifestExists {
		existingIndex.Manifests = append(existingIndex.Manifests, manifestDesc)
		if err := s.saveIndex(existingIndex); err != nil {
			return nil, fmt.Errorf("update index: %w", err)
		}
	}

	// Add tag if provided
	if ref != "" {
		// Tag reference (implies :latest when no tag is provided).
		if strings.Contains(ref, "@") || isDigestReference(ref) {
			return nil, fmt.Errorf("invalid tag reference: %s", ref)
		}
		ref = normalizeTagRef(ref)

		repos, err := s.loadRepositories()
		if err != nil {
			return nil, err
		}
		repos.Refs[ref] = manifestDesc.Digest
		if err := s.saveRepositories(repos); err != nil {
			return nil, fmt.Errorf("update repositories: %w", err)
		}
	}

	// Build and return image
	var tags []string
	if ref != "" {
		tags = []string{ref}
	}
	return s.buildImage(manifestDesc.Digest, tags)
}

func selectManifestDescriptor(index *ocispec.Index) (ocispec.Descriptor, error) {
	if index == nil || len(index.Manifests) == 0 {
		return ocispec.Descriptor{}, fmt.Errorf("invalid OCI index: no manifests")
	}
	if len(index.Manifests) == 1 {
		return index.Manifests[0], nil
	}

	// Target platform for this project is linux/amd64 (rootful).
	for _, desc := range index.Manifests {
		if desc.Platform != nil &&
			desc.Platform.OS == "linux" &&
			desc.Platform.Architecture == "amd64" {
			return desc, nil
		}
	}

	return ocispec.Descriptor{}, fmt.Errorf("multi-platform OCI index is not supported: no linux/amd64 manifest found")
}

// extractBlob extracts a blob from the tar reader and stores it.
// Returns the verified digest and size.
func (s *imageStore) extractBlob(r io.Reader, tarPath string) (digest.Digest, int64, error) {
	// Extract expected digest from path
	// Format: blobs/<algorithm>/<encoded>
	parts := strings.Split(tarPath, "/")
	if len(parts) != 3 {
		return "", 0, fmt.Errorf("invalid blob path: %s", tarPath)
	}
	algorithm := parts[1]
	encoded := parts[2]
	expectedDigest, err := digest.Parse(algorithm + ":" + encoded)
	if err != nil {
		return "", 0, fmt.Errorf("invalid digest in path: %w", err)
	}

	// Check if blob already exists (deduplication)
	if s.HasBlob(expectedDigest) {
		// Consume the reader but don't store
		size, err := io.Copy(io.Discard, r)
		if err != nil {
			return "", 0, fmt.Errorf("read blob: %w", err)
		}
		return expectedDigest, size, nil
	}

	// Write to temp file while verifying digest.
	// Important: do NOT write unverified content into CAS, and do NOT delete any existing blob on mismatch.
	tmpFile, err := os.CreateTemp(s.root, "blob-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	// Compute digest using the expected algorithm while writing.
	digester := expectedDigest.Algorithm().Digester()
	mw := io.MultiWriter(tmpFile, digester.Hash())
	size, err := io.Copy(mw, r)
	if err != nil {
		return "", 0, fmt.Errorf("write blob: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", 0, fmt.Errorf("close temp file: %w", err)
	}

	actualDigest := digester.Digest()
	if actualDigest != expectedDigest {
		return "", 0, fmt.Errorf("digest mismatch: expected %s, got %s", expectedDigest, actualDigest)
	}

	// Move to final location under the expected digest (CAS).
	blobPath := s.blobPath(expectedDigest)
	if err := os.MkdirAll(filepath.Dir(blobPath), 0755); err != nil {
		return "", 0, fmt.Errorf("create blob directory: %w", err)
	}
	if err := os.Rename(tmpPath, blobPath); err != nil {
		return "", 0, fmt.Errorf("move blob: %w", err)
	}

	return expectedDigest, size, nil
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

// ExportOCITar exports an image to an OCI tar archive.
// Reserved for future implementation.
func (s *imageStore) ExportOCITar(ref string, w io.Writer) error {
	dgst, err := s.resolveReference(ref)
	if err != nil {
		return err
	}

	manifest, err := s.GetManifest(dgst)
	if err != nil {
		return err
	}

	tw := tar.NewWriter(w)
	defer tw.Close()

	// Write oci-layout
	layout := ImageLayout{ImageLayoutVersion: ImageLayoutVersion}
	layoutData, _ := json.MarshalIndent(layout, "", "  ")
	if err := writeTarEntry(tw, ImageLayoutFile, layoutData); err != nil {
		return err
	}

	// Collect all blobs to export
	blobs := []digest.Digest{dgst, manifest.Config.Digest}
	for _, layer := range manifest.Layers {
		blobs = append(blobs, layer.Digest)
	}

	// Write blobs
	for _, blob := range blobs {
		blobPath := filepath.Join(BlobsDir, blob.Algorithm().String(), blob.Encoded())
		r, err := s.GetBlob(blob)
		if err != nil {
			return fmt.Errorf("get blob %s: %w", blob, err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return fmt.Errorf("read blob %s: %w", blob, err)
		}
		if err := writeTarEntry(tw, blobPath, data); err != nil {
			return err
		}
	}

	// Get size of manifest blob for descriptor
	manifestBlobPath := s.blobPath(dgst)
	fi, err := os.Stat(manifestBlobPath)
	if err != nil {
		return fmt.Errorf("stat manifest: %w", err)
	}

	// Write index.json
	index := ocispec.Index{
		Versioned: ocispec.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    dgst,
				Size:      fi.Size(),
			},
		},
	}
	indexData, _ := json.MarshalIndent(index, "", "  ")
	if err := writeTarEntry(tw, ImageIndexFile, indexData); err != nil {
		return err
	}

	return nil
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar content for %s: %w", name, err)
	}
	return nil
}
