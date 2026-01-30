//go:build integration && linux
// +build integration,linux

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVolumeCreate tests creating a named volume.
func TestVolumeCreate(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a volume
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "volume", "create", "testvol")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume create failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "testvol") {
		t.Errorf("expected output to contain volume name, got: %s", output)
	}

	// Verify volume directory was created
	volumeDir := filepath.Join(stateRoot, "volumes", "testvol", "_data")
	if _, err := os.Stat(volumeDir); os.IsNotExist(err) {
		t.Errorf("volume data directory was not created: %s", volumeDir)
	}

	// Verify volume can be listed
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "testvol") {
		t.Errorf("expected volume list to contain testvol, got: %s", output)
	}
}

// TestVolumeLs tests listing volumes.
func TestVolumeLs(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create multiple volumes
	for _, name := range []string{"vol1", "vol2", "vol3"} {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "volume", "create", name)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create volume %s failed: %v\nOutput: %s", name, err, output)
		}
	}

	// List volumes in table format
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls failed: %v\nOutput: %s", err, output)
	}

	for _, name := range []string{"vol1", "vol2", "vol3"} {
		if !strings.Contains(string(output), name) {
			t.Errorf("expected volume list to contain %s, got: %s", name, output)
		}
	}

	// List volumes in quiet mode
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls", "-q")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls -q failed: %v\nOutput: %s", err, output)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 volumes in quiet output, got %d: %s", len(lines), output)
	}

	// List volumes in JSON format
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls", "--format", "json")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls --format json failed: %v\nOutput: %s", err, output)
	}

	var volumes []map[string]interface{}
	if err := json.Unmarshal(output, &volumes); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput: %s", err, output)
	}

	if len(volumes) != 3 {
		t.Errorf("expected 3 volumes in JSON output, got %d", len(volumes))
	}
}

// TestVolumeRm tests removing a volume.
func TestVolumeRm(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a volume
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "volume", "create", "todelete")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("volume create failed: %v\nOutput: %s", err, output)
	}

	// Remove the volume
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "rm", "todelete")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume rm failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "todelete") {
		t.Errorf("expected output to contain removed volume name, got: %s", output)
	}

	// Verify volume directory was removed
	volumeDir := filepath.Join(stateRoot, "volumes", "todelete")
	if _, err := os.Stat(volumeDir); !os.IsNotExist(err) {
		t.Errorf("volume directory should be removed: %s", volumeDir)
	}

	// Verify volume is no longer listed
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls", "-q")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls failed: %v\nOutput: %s", err, output)
	}

	if strings.Contains(string(output), "todelete") {
		t.Errorf("removed volume should not appear in list: %s", output)
	}
}

// TestBindMountBasic tests basic bind mount functionality.
func TestBindMountBasic(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Create a host directory with a test file
	hostDir := filepath.Join(t.TempDir(), "hostdata")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("create host directory: %v", err)
	}
	testFile := filepath.Join(hostDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello_from_host"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Run container with bind mount and read the file
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", hostDir+":/data",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "cat /data/test.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with bind mount failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello_from_host") {
		t.Errorf("expected to see 'hello_from_host' from mounted file, got: %s", output)
	}

	removeAllContainers(t, stateRoot)
}

// TestBindMountWrite tests writing to a bind mount.
func TestBindMountWrite(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Create a host directory
	hostDir := filepath.Join(t.TempDir(), "hostdata")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("create host directory: %v", err)
	}

	// Run container with bind mount and write a file
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", hostDir+":/data",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "echo container_write > /data/output.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with bind mount failed: %v\nOutput: %s", err, output)
	}

	// Verify the file was written on the host
	outputFile := filepath.Join(hostDir, "output.txt")
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file on host: %v", err)
	}

	if !strings.Contains(string(content), "container_write") {
		t.Errorf("expected output file to contain 'container_write', got: %s", content)
	}

	removeAllContainers(t, stateRoot)
}

// TestBindMountReadOnly tests read-only bind mounts.
func TestBindMountReadOnly(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Create a host directory with a file
	hostDir := filepath.Join(t.TempDir(), "hostdata")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("create host directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "existing.txt"), []byte("existing"), 0644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	// Run container with read-only bind mount and try to write
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", hostDir+":/data:ro",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "echo test > /data/newfile.txt")
	output, err := cmd.CombinedOutput()

	// The write should fail due to read-only mount
	if err == nil {
		t.Errorf("expected write to read-only mount to fail, but it succeeded")
	}

	// Verify the file was not created
	newFile := filepath.Join(hostDir, "newfile.txt")
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Errorf("file should not be created on read-only mount")
	}

	removeAllContainers(t, stateRoot)
}

// TestNamedVolumeAutoCreate tests auto-creation of named volumes.
func TestNamedVolumeAutoCreate(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Run container with a named volume that doesn't exist yet
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", "autovolume:/data",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "echo auto > /data/test.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with named volume failed: %v\nOutput: %s", err, output)
	}

	// Verify volume was auto-created
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "volume", "ls", "-q")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volume ls failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "autovolume") {
		t.Errorf("expected volume to be auto-created, got: %s", output)
	}

	// Verify data was written to volume
	volumeDataDir := filepath.Join(stateRoot, "volumes", "autovolume", "_data")
	content, err := os.ReadFile(filepath.Join(volumeDataDir, "test.txt"))
	if err != nil {
		t.Fatalf("read volume data: %v", err)
	}

	if !strings.Contains(string(content), "auto") {
		t.Errorf("expected volume data to contain 'auto', got: %s", content)
	}

	removeAllContainers(t, stateRoot)
}

// TestNamedVolumePersistence tests that named volume data survives container removal.
func TestNamedVolumePersistence(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Create a named volume
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "volume", "create", "persistvol")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("volume create failed: %v\nOutput: %s", err, output)
	}

	// Container 1: Write data to volume
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", "persistvol:/data",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "echo persistent_data > /data/file.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("container 1 run failed: %v\nOutput: %s", err, output)
	}

	// Remove all containers
	removeAllContainers(t, stateRoot)

	// Container 2: Read data from the same volume
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", "persistvol:/data",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "cat /data/file.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("container 2 run failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "persistent_data") {
		t.Errorf("expected to see 'persistent_data' from persisted volume, got: %s", output)
	}

	removeAllContainers(t, stateRoot)
}

// TestMultipleVolumeMounts tests mounting multiple volumes.
func TestMultipleVolumeMounts(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create a minimal rootfs
	rootfsDir := prepareMinimalRootfs(t)
	t.Cleanup(func() { _ = os.RemoveAll(rootfsDir) })

	// Create host directories
	hostDir1 := filepath.Join(t.TempDir(), "data1")
	hostDir2 := filepath.Join(t.TempDir(), "data2")
	os.MkdirAll(hostDir1, 0755)
	os.MkdirAll(hostDir2, 0755)
	os.WriteFile(filepath.Join(hostDir1, "file1.txt"), []byte("data1"), 0644)
	os.WriteFile(filepath.Join(hostDir2, "file2.txt"), []byte("data2"), 0644)

	// Run container with multiple bind mounts
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", hostDir1+":/mnt1",
		"-v", hostDir2+":/mnt2",
		"--rootfs", rootfsDir,
		"/bin/sh", "-c", "cat /mnt1/file1.txt && cat /mnt2/file2.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run with multiple mounts failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "data1") || !strings.Contains(string(output), "data2") {
		t.Errorf("expected to see both 'data1' and 'data2', got: %s", output)
	}

	removeAllContainers(t, stateRoot)
}

// TestVolumeWithImage tests volume mounts with image-based containers.
func TestVolumeWithImage(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// Create and load a test image
	tarPath := filepath.Join(t.TempDir(), "test-image.tar")
	createTestOCITarWithRootfs(t, tarPath)

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "load", "-i", tarPath, "-t", "test:v1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load failed: %v\nOutput: %s", err, output)
	}

	// Create a host directory with test data
	hostDir := filepath.Join(t.TempDir(), "hostdata")
	os.MkdirAll(hostDir, 0755)
	os.WriteFile(filepath.Join(hostDir, "test.txt"), []byte("image_volume_test"), 0644)

	// Run image container with bind mount
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-v", hostDir+":/data",
		"test:v1",
		"/bin/sh", "-c", "cat /data/test.txt")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run image with volume failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "image_volume_test") {
		t.Errorf("expected to see 'image_volume_test', got: %s", output)
	}

	removeAllContainers(t, stateRoot)
}
