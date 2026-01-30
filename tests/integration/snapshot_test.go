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

// TestRunWithImage tests running a container with an image.
func TestRunWithImage(t *testing.T) {
	skipIfNotRoot(t)

	// Create a temporary state root for isolation
	stateRoot := t.TempDir()

	// Create a test OCI image tar
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	createTestOCITarWithRootfs(t, tarPath)

	// Load the test image
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load failed: %v\nOutput: %s", err, output)
	}

	// Run container with image (shell should work)
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "test:v1", "/bin/sh", "-c", "echo hello_from_image")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with image failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(string(output), "hello_from_image") {
		t.Fatalf("expected output to contain hello_from_image, got: %s", output)
	}

	// Verify snapshot directories were created
	snapshotsDir := filepath.Join(stateRoot, "snapshots")
	if _, err := os.Stat(snapshotsDir); os.IsNotExist(err) {
		t.Errorf("snapshots directory was not created")
	}

	// Cleanup: remove created container(s) to unmount overlay and remove upper/work.
	removeAllContainers(t, stateRoot)
}

// TestRunWithRootfsStillWorks verifies backward compatibility with --rootfs flag.
func TestRunWithRootfsStillWorks(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Run with --rootfs (old way)
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "--rootfs", rootfsDir, "/bin/sh", "-c", "echo hello_rootfs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with --rootfs failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello_rootfs") {
		t.Errorf("expected 'hello_rootfs' in output, got: %s", output)
	}

	removeAllContainers(t, stateRoot)
}

// TestMultipleContainersShareLayers verifies that layer cache is shared.
func TestMultipleContainersShareLayers(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create and load a test image
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	createTestOCITarWithRootfs(t, tarPath)

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load failed: %v\nOutput: %s", err, output)
	}

	// Start first container (detached so we can clean up deterministically)
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "test:v1", "/bin/sh", "-c", "while true; do :; done")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run -d failed: %v\nOutput: %s", err, out)
	}
	id1 := strings.TrimSpace(string(out))
	if id1 == "" {
		t.Fatalf("expected container id from detached run, got empty output: %s", out)
	}

	// Check that layers directory exists
	layersDir := filepath.Join(stateRoot, "snapshots", "layers", "sha256")
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		t.Fatalf("failed to read layers directory: %v", err)
	}

	initialLayerCount := len(entries)

	// Start second container
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "test:v1", "/bin/sh", "-c", "while true; do :; done")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("second run -d failed: %v\nOutput: %s", err, out)
	}
	id2 := strings.TrimSpace(string(out))
	if id2 == "" {
		t.Fatalf("expected container id from second detached run, got empty output: %s", out)
	}

	// Verify layer count hasn't increased (layers are shared)
	entries, err = os.ReadDir(layersDir)
	if err != nil {
		t.Fatalf("failed to read layers directory: %v", err)
	}

	if len(entries) != initialLayerCount {
		t.Errorf("expected layer count to remain %d, got %d (layers should be shared)",
			initialLayerCount, len(entries))
	}

	// Cleanup
	_ = exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", id1).Run()
	_ = exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", id2).Run()
}

// TestRmCleansUpSnapshot verifies that rm cleans up overlay mount and upper/work dirs.
func TestRmCleansUpSnapshot(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create and load a test image
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	createTestOCITarWithRootfs(t, tarPath)

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load failed: %v\nOutput: %s", err, output)
	}

	// Start a detached container
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "test:v1", "/bin/sh", "-c", "while true; do :; done")
	output, err := cmd.CombinedOutput()
	containerID := strings.TrimSpace(string(output))

	if err != nil {
		t.Fatalf("run -d failed: %v\nOutput: %s", err, output)
	}
	if containerID == "" {
		t.Fatalf("expected container id from detached run, got empty output: %s", output)
	}

	// Check snapshot container directory exists (if container started successfully)
	snapshotContainerDir := filepath.Join(stateRoot, "snapshots", "containers", containerID)
	if _, err := os.Stat(snapshotContainerDir); err != nil {
		t.Fatalf("expected snapshot container dir to exist: %v", err)
	}

	// Remove the container
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm failed: %v\nOutput: %s", err, output)
	}

	// Verify snapshot container directory is cleaned up
	if _, err := os.Stat(snapshotContainerDir); !os.IsNotExist(err) {
		t.Errorf("snapshot container directory should be removed after rm: %s", snapshotContainerDir)
	}

	// Verify layers are preserved
	layersDir := filepath.Join(stateRoot, "snapshots", "layers", "sha256")
	if _, err := os.Stat(layersDir); os.IsNotExist(err) {
		t.Errorf("layers directory should be preserved after rm")
	}
}

// TestSnapshotPreparationFailsGracefully tests error handling when image doesn't exist.
func TestSnapshotPreparationFailsGracefully(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Try to run with non-existent image
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "nonexistent:image", "/bin/sh")
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Errorf("expected error when running with non-existent image")
	}

	if !strings.Contains(string(output), "not found") {
		t.Errorf("expected 'not found' error, got: %s", output)
	}
}

// TestWritableLayerIsolation verifies that container writes don't affect other containers.
func TestWritableLayerIsolation(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create and load a runnable test image
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	createTestOCITarWithRootfs(t, tarPath)
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load failed: %v\nOutput: %s", err, output)
	}

	// Container 1: create a file in upper layer
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "test:v1", "/bin/sh", "-c", "echo hi > /foo && cat /foo")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("container1 run failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(string(out), "hi") {
		t.Fatalf("expected container1 to print 'hi', got: %s", out)
	}

	// Container 2: ensure the file does NOT exist
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "test:v1", "/bin/sh", "-c", "test ! -e /foo")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("container2 should not see /foo (expected success), got err=%v output=%s", err, out)
	}

	removeAllContainers(t, stateRoot)
}

// setupMinimalRootfs creates a minimal rootfs for testing.
// This function is also used by other tests.
func setupMinimalRootfs(t *testing.T, rootfsDir string) {
	t.Helper()

	// Create minimal directory structure
	dirs := []string{"bin", "dev", "etc", "proc", "sys", "tmp"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(rootfsDir, dir), 0755); err != nil {
			t.Fatalf("create directory %s: %v", dir, err)
		}
	}

	// Copy busybox if available
	busyboxPath := "/bin/busybox"
	if _, err := os.Stat(busyboxPath); err == nil {
		destPath := filepath.Join(rootfsDir, "bin", "busybox")
		input, err := os.ReadFile(busyboxPath)
		if err != nil {
			t.Fatalf("read busybox: %v", err)
		}
		if err := os.WriteFile(destPath, input, 0755); err != nil {
			t.Fatalf("write busybox: %v", err)
		}

		// Create symlinks for common commands
		for _, cmd := range []string{"sh", "echo", "cat", "ls"} {
			cmdPath := filepath.Join(rootfsDir, "bin", cmd)
			os.Symlink("busybox", cmdPath)
		}
	} else {
		// If busybox is not available, copy system binaries
		for _, cmd := range []string{"sh", "echo"} {
			srcPath := "/bin/" + cmd
			if _, err := os.Stat(srcPath); err == nil {
				destPath := filepath.Join(rootfsDir, "bin", cmd)
				input, err := os.ReadFile(srcPath)
				if err != nil {
					continue
				}
				os.WriteFile(destPath, input, 0755)
			}
		}
	}
}

func removeAllContainers(t *testing.T, stateRoot string) {
	t.Helper()

	containersDir := filepath.Join(stateRoot, "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		_ = exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", e.Name()).Run()
	}
}

// createTestOCITarWithRootfs creates a runnable OCI tar archive by packing a minimal rootfs
// into a single uncompressed OCI layer.
func createTestOCITarWithRootfs(t *testing.T, tarPath string) digest.Digest {
	t.Helper()

	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	layerContent := createLayerTarFromDir(t, rootfsDir)
	layerDigest := digest.FromBytes(layerContent)

	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar file failed: %v", err)
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	defer tw.Close()

	// 1. oci-layout
	layout := `{"imageLayoutVersion":"1.0.0"}`
	writeTestTarEntry(t, tw, "oci-layout", []byte(layout))

	// 2. layer blob (uncompressed tar)
	writeTestTarEntry(t, tw, "blobs/sha256/"+layerDigest.Encoded(), layerContent)

	// 3. config
	cfg := ocispec.Image{
		Architecture: "amd64",
		OS:           "linux",
		RootFS: ocispec.RootFS{
			Type:    "layers",
			DiffIDs: []digest.Digest{layerDigest},
		},
	}
	cfgBytes, _ := json.Marshal(cfg)
	cfgDigest := digest.FromBytes(cfgBytes)
	writeTestTarEntry(t, tw, "blobs/sha256/"+cfgDigest.Encoded(), cfgBytes)

	// 4. manifest
	manifest := ocispec.Manifest{
		Versioned: ocispec.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    cfgDigest,
			Size:      int64(len(cfgBytes)),
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

	// 5. index.json
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

func createLayerTarFromDir(t *testing.T, rootfsDir string) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.WalkDir(rootfsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(rootfsDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		// Normalize mode bits; avoid embedding host uid/gid/mtime into test artifacts.
		var hdr *tar.Header
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr = &tar.Header{
				Name:     rel,
				Typeflag: tar.TypeSymlink,
				Linkname: linkTarget,
				Mode:     0777,
			}
		} else if info.IsDir() {
			name := rel
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
			hdr = &tar.Header{
				Name:     name,
				Typeflag: tar.TypeDir,
				Mode:     int64(info.Mode().Perm()),
			}
		} else if info.Mode().IsRegular() {
			hdr = &tar.Header{
				Name:     rel,
				Typeflag: tar.TypeReg,
				Mode:     int64(info.Mode().Perm()),
				Size:     info.Size(),
			}
		} else {
			// Skip special files (devices, sockets, etc.)
			return nil
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			_ = f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = tw.Close()
		t.Fatalf("failed to create layer tar: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}
