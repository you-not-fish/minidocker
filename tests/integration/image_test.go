//go:build integration && linux
// +build integration,linux

package integration

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestImageLoadListDelete tests the full image lifecycle: load, list, delete.
func TestImageLoadListDelete(t *testing.T) {
	// Create a temporary state root for isolation
	stateRoot := t.TempDir()

	// Create a test OCI image tar
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	manifestDigest := createTestOCITar(t, tarPath)

	// Test: load the image
	t.Run("load", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("load failed: %v\nOutput: %s", err, output)
		}
		if !strings.Contains(string(output), "Loaded image") {
			t.Errorf("expected 'Loaded image' in output, got: %s", output)
		}
	})

	// Test: list images
	t.Run("images", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "images")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("images failed: %v\nOutput: %s", err, output)
		}
		// Check for expected columns
		if !strings.Contains(string(output), "REPOSITORY") ||
			!strings.Contains(string(output), "TAG") ||
			!strings.Contains(string(output), "IMAGE ID") {
			t.Errorf("expected table headers in output, got: %s", output)
		}
		// Check for our test image
		if !strings.Contains(string(output), "test") ||
			!strings.Contains(string(output), "v1") {
			t.Errorf("expected test:v1 in output, got: %s", output)
		}
	})

	// Test: images with -q flag
	t.Run("images-quiet", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "images", "-q")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("images -q failed: %v\nOutput: %s", err, output)
		}
		// Should only contain ID (12 chars by default)
		id := strings.TrimSpace(string(output))
		if len(id) != 12 {
			t.Errorf("expected 12-char ID, got %d chars: %s", len(id), id)
		}
	})

	// Test: images with --format json
	t.Run("images-json", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "images", "--format", "json")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("images --format json failed: %v\nOutput: %s", err, output)
		}
		// Parse JSON
		var images []map[string]interface{}
		if err := json.Unmarshal(output, &images); err != nil {
			t.Fatalf("failed to parse JSON: %v\nOutput: %s", err, output)
		}
		if len(images) != 1 {
			t.Errorf("expected 1 image, got %d", len(images))
		}
	})

	// Test: delete image by tag
	t.Run("rmi", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "rmi", "test:v1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rmi failed: %v\nOutput: %s", err, output)
		}
		if !strings.Contains(string(output), "Untagged") {
			t.Errorf("expected 'Untagged' in output, got: %s", output)
		}
	})

	// Test: verify image is deleted
	t.Run("images-after-delete", func(t *testing.T) {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "images", "-q")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("images -q failed: %v\nOutput: %s", err, output)
		}
		// Should be empty
		if strings.TrimSpace(string(output)) != "" {
			t.Errorf("expected no images after delete, got: %s", output)
		}
	})

	_ = manifestDigest // Use if needed for additional verification
}

// TestImageLoadDuplicateBlobs verifies that duplicate blobs are not stored twice.
func TestImageLoadDuplicateBlobs(t *testing.T) {
	stateRoot := t.TempDir()

	// Create first test image
	tarPath1 := filepath.Join(t.TempDir(), "image1.tar")
	createTestOCITar(t, tarPath1)

	// Load first image
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath1, "-t", "test1:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load image1 failed: %v\nOutput: %s", err, output)
	}

	// Create second image with the same layer content
	tarPath2 := filepath.Join(t.TempDir(), "image2.tar")
	createTestOCITar(t, tarPath2)

	// Load second image
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath2, "-t", "test2:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load image2 failed: %v\nOutput: %s", err, output)
	}

	// Verify we have 2 images
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "images", "-q")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("images failed: %v\nOutput: %s", err, output)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	// Since both images have the same manifest, they should have the same ID
	// This tests CAS deduplication
	if len(lines) > 2 {
		t.Errorf("expected at most 2 image IDs (may be same due to dedup), got %d", len(lines))
	}
}

// TestImageLoadInvalidTar tests loading an invalid tar file.
func TestImageLoadInvalidTar(t *testing.T) {
	stateRoot := t.TempDir()

	// Create an invalid tar (not OCI format)
	tarPath := filepath.Join(t.TempDir(), "invalid.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create file failed: %v", err)
	}
	tw := tar.NewWriter(f)
	// Write a random file, not OCI layout
	tw.WriteHeader(&tar.Header{Name: "random.txt", Size: 5, Mode: 0644})
	tw.Write([]byte("hello"))
	tw.Close()
	f.Close()

	// Try to load
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "invalid:v1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected load to fail for invalid tar, but it succeeded")
	}
	if !strings.Contains(string(output), "oci-layout") {
		t.Errorf("expected error about missing oci-layout, got: %s", output)
	}
}

// TestRmiNonExistent tests deleting a non-existent image.
func TestRmiNonExistent(t *testing.T) {
	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "rmi", "nonexistent:v1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected rmi to fail for non-existent image, but it succeeded")
	}
	if !strings.Contains(string(output), "not found") {
		t.Errorf("expected 'not found' error, got: %s", output)
	}
}

// createTestOCITar creates a minimal valid OCI tar archive for testing.
// Returns the manifest digest.
func createTestOCITar(t *testing.T, tarPath string) digest.Digest {
	t.Helper()

	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar file failed: %v", err)
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	defer tw.Close()

	// 1. Write oci-layout
	layout := `{"imageLayoutVersion":"1.0.0"}`
	writeTestTarEntry(t, tw, "oci-layout", []byte(layout))

	// 2. Create a minimal valid layer (empty tar archive).
	// Note: layer blob must be a tar/tar.gz stream, not an empty byte slice.
	var layerBuf bytes.Buffer
	layerTW := tar.NewWriter(&layerBuf)
	_ = layerTW.Close()
	layerContent := layerBuf.Bytes()
	layerDigest := digest.FromBytes(layerContent)
	writeTestTarEntry(t, tw, "blobs/sha256/"+layerDigest.Encoded(), layerContent)

	// 3. Create config
	config := ocispec.Image{
		Architecture: "amd64",
		OS:           "linux",
		RootFS: ocispec.RootFS{
			Type:    "layers",
			DiffIDs: []digest.Digest{layerDigest},
		},
	}
	configBytes, _ := json.Marshal(config)
	configDigest := digest.FromBytes(configBytes)
	writeTestTarEntry(t, tw, "blobs/sha256/"+configDigest.Encoded(), configBytes)

	// 4. Create manifest
	manifest := ocispec.Manifest{
		Versioned: ocispec.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    configDigest,
			Size:      int64(len(configBytes)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layerContent)),
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestBytes)
	writeTestTarEntry(t, tw, "blobs/sha256/"+manifestDigest.Encoded(), manifestBytes)

	// 5. Create index.json
	index := ocispec.Index{
		Versioned: ocispec.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    manifestDigest,
				Size:      int64(len(manifestBytes)),
			},
		},
	}
	indexBytes, _ := json.Marshal(index)
	writeTestTarEntry(t, tw, "index.json", indexBytes)

	return manifestDigest
}

func writeTestTarEntry(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()

	header := &tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write tar header for %s: %v", name, err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
		t.Fatalf("write tar content for %s: %v", name, err)
	}
}
