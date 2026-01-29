// Package fileutil provides file operation utilities.
//
// This package contains common file operations used across minidocker,
// including atomic file writes that prevent partial writes and data corruption.
package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to a file atomically.
//
// It first writes to a temporary file in the same directory, then renames
// it to the target path. This ensures that the file is either fully written
// or not written at all, preventing partial writes.
//
// The temporary file is created with .tmp suffix and is cleaned up on error.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	// Create temporary file in the same directory to ensure atomic rename
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Clean up temporary file on rename failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temporary file: %w", err)
	}

	return nil
}

// EnsureDir ensures that a directory exists, creating it if necessary.
// It creates all parent directories as needed with the specified permissions.
func EnsureDir(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", path, err)
	}
	return nil
}

// EnsureParentDir ensures that the parent directory of the given path exists.
func EnsureParentDir(path string, perm os.FileMode) error {
	return EnsureDir(filepath.Dir(path), perm)
}
