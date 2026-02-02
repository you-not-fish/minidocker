//go:build integration && linux
// +build integration,linux

package integration

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPullBasic tests pulling a small image from Docker Hub.
func TestPullBasic(t *testing.T) {
	// Skip if network is not available
	if !isNetworkAvailable() {
		t.Skip("skipping: network not available")
	}

	stateRoot := t.TempDir()

	// Pull a small image (busybox is ~1MB)
	// Note: We use busybox instead of alpine because it's smaller and faster to download
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "pull", "busybox:musl")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir()) // Isolate auth config
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pull failed: %v\nOutput: %s", err, output)
	}

	// Verify output contains expected messages
	outputStr := string(output)
	if !strings.Contains(outputStr, "Pulling") {
		t.Errorf("expected 'Pulling' in output, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "Pulled:") {
		t.Errorf("expected 'Pulled:' in output, got: %s", outputStr)
	}

	// Verify image is in local store
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "images")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("images failed: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(string(output), "busybox") {
		t.Errorf("expected 'busybox' in images output, got: %s", output)
	}
}

// TestPullQuiet tests pulling with quiet mode.
func TestPullQuiet(t *testing.T) {
	if !isNetworkAvailable() {
		t.Skip("skipping: network not available")
	}

	stateRoot := t.TempDir()

	// Pull with quiet mode
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "pull", "-q", "busybox:musl")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pull -q failed: %v\nOutput: %s", err, output)
	}

	// In quiet mode, output should only be the digest (sha256 encoded)
	outputStr := strings.TrimSpace(string(output))
	if len(outputStr) != 64 {
		t.Errorf("expected 64-char digest in quiet mode, got %d chars: %s", len(outputStr), outputStr)
	}
}

// TestPullAlreadyExists tests pulling an image that already exists.
func TestPullAlreadyExists(t *testing.T) {
	if !isNetworkAvailable() {
		t.Skip("skipping: network not available")
	}

	stateRoot := t.TempDir()

	// Pull image first time
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "pull", "busybox:musl")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("first pull failed: %v\nOutput: %s", err, output)
	}

	// Pull same image again
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "pull", "busybox:musl")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("second pull failed: %v\nOutput: %s", err, output)
	}

	// Should indicate layers already exist
	outputStr := string(output)
	if !strings.Contains(outputStr, "exists") && !strings.Contains(outputStr, "Pulled") {
		t.Logf("Note: second pull output: %s", outputStr)
	}
}

// TestPullInvalidImage tests pulling a non-existent image.
func TestPullInvalidImage(t *testing.T) {
	if !isNetworkAvailable() {
		t.Skip("skipping: network not available")
	}

	stateRoot := t.TempDir()

	// Try to pull a non-existent image
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "pull", "nonexistent-image-12345:v999")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected pull to fail for non-existent image, but it succeeded")
	}
	// Should contain error message
	outputStr := string(output)
	if !strings.Contains(strings.ToLower(outputStr), "not found") &&
		!strings.Contains(strings.ToLower(outputStr), "manifest unknown") &&
		!strings.Contains(strings.ToLower(outputStr), "name unknown") {
		t.Logf("Error output: %s", outputStr)
	}
}

// TestPullAndRun tests pulling an image and running a container from it.
func TestPullAndRun(t *testing.T) {
	if !isNetworkAvailable() {
		t.Skip("skipping: network not available")
	}

	stateRoot := t.TempDir()

	// Pull image
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "pull", "busybox:musl")
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pull failed: %v\nOutput: %s", err, output)
	}

	// Run container from pulled image
	cmd = exec.Command(minidockerBin, "--root", stateRoot, "run", "--network", "none", "busybox:musl", "/bin/echo", "hello from pulled image")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello from pulled image") {
		t.Errorf("expected 'hello from pulled image' in output, got: %s", output)
	}
}

// isNetworkAvailable checks if external network is accessible.
func isNetworkAvailable() bool {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	// Try to reach Docker Hub API
	resp, err := client.Get("https://registry-1.docker.io/v2/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	// 401 is expected (unauthenticated), but it means the registry is reachable
	return resp.StatusCode == 401 || resp.StatusCode == 200
}
