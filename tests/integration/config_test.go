//go:build integration && linux
// +build integration,linux

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvVars tests the -e/--env flag
func TestEnvVars(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	tests := []struct {
		name     string
		envFlags []string
		command  string
		expected string
	}{
		{
			name:     "single env var",
			envFlags: []string{"-e", "FOO=bar"},
			command:  "echo $FOO",
			expected: "bar",
		},
		{
			name:     "multiple env vars",
			envFlags: []string{"-e", "FOO=bar", "-e", "BAZ=qux"},
			command:  "echo $FOO-$BAZ",
			expected: "bar-qux",
		},
		{
			name:     "env var with spaces",
			envFlags: []string{"-e", "MSG=hello world"},
			command:  "echo $MSG",
			expected: "hello world",
		},
		{
			name:     "env var with equals sign",
			envFlags: []string{"-e", "URL=http://example.com?foo=bar"},
			command:  "echo $URL",
			expected: "http://example.com?foo=bar",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"run", "--root", rootDir, "--rootfs", rootfs}
			args = append(args, tc.envFlags...)
			args = append(args, "/bin/sh", "-c", tc.command)

			cmd := exec.Command(minidockerBin, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("command failed: %v\noutput: %s", err, output)
			}

			got := strings.TrimSpace(string(output))
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// TestHostname tests the --hostname flag
func TestHostname(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	tests := []struct {
		name     string
		hostname string
		expected string
	}{
		{
			name:     "custom hostname",
			hostname: "mycontainer",
			expected: "mycontainer",
		},
		{
			name:     "hostname with numbers",
			hostname: "host123",
			expected: "host123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"run", "--root", rootDir, "--rootfs", rootfs, "--hostname", tc.hostname, "/bin/hostname"}

			cmd := exec.Command(minidockerBin, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("command failed: %v\noutput: %s", err, output)
			}

			got := strings.TrimSpace(string(output))
			if got != tc.expected {
				t.Errorf("expected hostname %q, got %q", tc.expected, got)
			}
		})
	}
}

// TestWorkDir tests the -w/--workdir flag
func TestWorkDir(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	// Create a test directory in rootfs
	testDir := filepath.Join(rootfs, "app")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	tests := []struct {
		name     string
		workdir  string
		command  string
		expected string
	}{
		{
			name:     "workdir /tmp",
			workdir:  "/tmp",
			command:  "pwd",
			expected: "/tmp",
		},
		{
			name:     "workdir /app",
			workdir:  "/app",
			command:  "pwd",
			expected: "/app",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"run", "--root", rootDir, "--rootfs", rootfs, "-w", tc.workdir, "/bin/sh", "-c", tc.command}

			cmd := exec.Command(minidockerBin, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("command failed: %v\noutput: %s", err, output)
			}

			got := strings.TrimSpace(string(output))
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// TestUserNumeric tests the -u/--user flag with numeric UID
func TestUserNumeric(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	tests := []struct {
		name        string
		user        string
		expectedUID string
		expectedGID string
	}{
		{
			name:        "uid 1000",
			user:        "1000",
			expectedUID: "1000",
			expectedGID: "1000",
		},
		{
			name:        "uid:gid 1000:1001",
			user:        "1000:1001",
			expectedUID: "1000",
			expectedGID: "1001",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"run", "--root", rootDir, "--rootfs", rootfs, "-u", tc.user, "/bin/sh", "-c", "id -u && id -g"}

			cmd := exec.Command(minidockerBin, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("command failed: %v\noutput: %s", err, output)
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			if len(lines) < 2 {
				t.Fatalf("expected 2 lines of output, got %d: %s", len(lines), output)
			}

			gotUID := strings.TrimSpace(lines[0])
			gotGID := strings.TrimSpace(lines[1])

			if gotUID != tc.expectedUID {
				t.Errorf("expected UID %q, got %q", tc.expectedUID, gotUID)
			}
			if gotGID != tc.expectedGID {
				t.Errorf("expected GID %q, got %q", tc.expectedGID, gotGID)
			}
		})
	}
}

// TestContainerName tests the --name flag
func TestContainerName(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	containerName := "my-test-container"

	// Run a detached container with a name
	args := []string{"run", "--root", rootDir, "--rootfs", rootfs, "--name", containerName, "-d", "/bin/sleep", "100"}
	cmd := exec.Command(minidockerBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\noutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Logf("created container %s with name %s", containerID[:12], containerName)

	// Verify we can reference by name using ps
	args = []string{"ps", "--root", rootDir, "-a"}
	cmd = exec.Command(minidockerBin, args...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ps failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(string(output), containerID[:12]) {
		t.Errorf("ps output does not contain container ID: %s", output)
	}

	// Stop container by name
	args = []string{"stop", "--root", rootDir, containerName}
	cmd = exec.Command(minidockerBin, args...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stop by name failed: %v\noutput: %s", err, output)
	}

	// Remove container by name
	args = []string{"rm", "--root", rootDir, containerName}
	cmd = exec.Command(minidockerBin, args...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm by name failed: %v\noutput: %s", err, output)
	}
}

// TestContainerNameConflict tests that duplicate names are rejected
func TestContainerNameConflict(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	containerName := "unique-name"

	// Create first container
	args := []string{"run", "--root", rootDir, "--rootfs", rootfs, "--name", containerName, "-d", "/bin/sleep", "100"}
	cmd := exec.Command(minidockerBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first run failed: %v\noutput: %s", err, output)
	}
	firstID := strings.TrimSpace(string(output))

	// Try to create second container with same name
	args = []string{"run", "--root", rootDir, "--rootfs", rootfs, "--name", containerName, "-d", "/bin/sleep", "100"}
	cmd = exec.Command(minidockerBin, args...)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error when using duplicate name, but got none")
	}

	if !strings.Contains(string(output), "already in use") {
		t.Errorf("expected 'already in use' error, got: %s", output)
	}

	// Cleanup
	exec.Command(minidockerBin, "stop", "--root", rootDir, firstID).Run()
	exec.Command(minidockerBin, "rm", "--root", rootDir, firstID).Run()
}

// TestCombinedConfig tests multiple config options together
func TestCombinedConfig(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	rootDir := setupTestRoot(t)
	defer os.RemoveAll(rootDir)
	rootfs := setupTestRootfs(t, rootDir)

	// Create /app directory
	appDir := filepath.Join(rootfs, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("failed to create app directory: %v", err)
	}

	// Run with multiple options
	args := []string{
		"run", "--root", rootDir, "--rootfs", rootfs,
		"-e", "APP_NAME=testapp",
		"-e", "APP_ENV=production",
		"-w", "/app",
		"-u", "1000:1000",
		"/bin/sh", "-c", "echo $APP_NAME-$APP_ENV && pwd && id -u",
	}

	cmd := exec.Command(minidockerBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\noutput: %s", err, output)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines of output, got %d: %s", len(lines), output)
	}

	// Check env vars
	if lines[0] != "testapp-production" {
		t.Errorf("expected env vars 'testapp-production', got %q", lines[0])
	}

	// Check workdir
	if lines[1] != "/app" {
		t.Errorf("expected workdir '/app', got %q", lines[1])
	}

	// Check uid
	if lines[2] != "1000" {
		t.Errorf("expected uid '1000', got %q", lines[2])
	}
}
