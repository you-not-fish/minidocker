//go:build linux
// +build linux

package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// containersDirName is the directory name for per-container snapshot data.
const containersDirName = "containers"

// upperDirName is the directory name for the container's writable layer.
const upperDirName = "upper"

// workDirName is the directory name for overlay's work directory.
const workDirName = "work"

// mountOverlay mounts an overlay filesystem.
// lowerDirs are the read-only layer paths (from bottom to top).
// upperDir is the writable layer path.
// workDir is the overlay work directory.
// mountPoint is where the merged view will be mounted.
func mountOverlay(lowerDirs []string, upperDir, workDir, mountPoint string) error {
	if len(lowerDirs) == 0 {
		return fmt.Errorf("at least one lower directory is required")
	}

	// Ensure all directories exist
	for _, dir := range lowerDirs {
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("lower directory not accessible: %s: %w", dir, err)
		}
	}

	if err := os.MkdirAll(upperDir, 0755); err != nil {
		return fmt.Errorf("create upper directory: %w", err)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}

	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	// OverlayFS lowerdir format: topmost layer first, colon-separated
	// Our lowerDirs are in extraction order (bottom to top), so we need to reverse
	reversedLowers := make([]string, len(lowerDirs))
	for i, dir := range lowerDirs {
		reversedLowers[len(lowerDirs)-1-i] = dir
	}

	// Build mount options
	// Format: lowerdir=top:...:bottom,upperdir=path,workdir=path
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		strings.Join(reversedLowers, ":"),
		upperDir,
		workDir,
	)

	// Mount the overlay
	if err := unix.Mount("overlay", mountPoint, "overlay", 0, options); err != nil {
		return fmt.Errorf("mount overlay: %w (options: %s)", err, options)
	}

	return nil
}

// unmountOverlay unmounts an overlay filesystem.
// Uses MNT_DETACH for lazy unmount to handle busy mount points.
func unmountOverlay(mountPoint string) error {
	// Check if mounted
	if !isMounted(mountPoint) {
		return nil // Already unmounted
	}

	// Try normal unmount first
	if err := unix.Unmount(mountPoint, 0); err != nil {
		// If busy, use lazy unmount
		if err == unix.EBUSY {
			return unix.Unmount(mountPoint, unix.MNT_DETACH)
		}
		return fmt.Errorf("unmount overlay: %w", err)
	}

	return nil
}

// isMounted checks if a path is a mount point.
func isMounted(path string) bool {
	// Get stat of the path and its parent
	pathStat, err := os.Stat(path)
	if err != nil {
		return false
	}

	parentPath := filepath.Dir(path)
	parentStat, err := os.Stat(parentPath)
	if err != nil {
		return false
	}

	// If the device numbers differ, it's a mount point
	pathSys, ok := pathStat.Sys().(*unix.Stat_t)
	if !ok {
		return false
	}

	parentSys, ok := parentStat.Sys().(*unix.Stat_t)
	if !ok {
		return false
	}

	return pathSys.Dev != parentSys.Dev
}

// containerSnapshotDir returns the path to a container's snapshot directory.
func (s *overlaySnapshotter) containerSnapshotDir(containerID string) string {
	return filepath.Join(s.root, containersDirName, containerID)
}

// containerUpperDir returns the path to a container's upper directory.
func (s *overlaySnapshotter) containerUpperDir(containerID string) string {
	return filepath.Join(s.containerSnapshotDir(containerID), upperDirName)
}

// containerWorkDir returns the path to a container's work directory.
func (s *overlaySnapshotter) containerWorkDir(containerID string) string {
	return filepath.Join(s.containerSnapshotDir(containerID), workDirName)
}

// mkfifo creates a named pipe (FIFO).
func mkfifo(path string, mode uint32) error {
	return unix.Mkfifo(path, mode)
}
