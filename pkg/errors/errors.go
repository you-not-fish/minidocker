// Package errors provides standard error types for minidocker.
//
// These sentinel errors allow callers to check for specific error conditions
// using errors.Is(), enabling programmatic error handling.
package errors

import "errors"

// Container lookup errors
var (
	// ErrContainerNotFound indicates the specified container does not exist.
	ErrContainerNotFound = errors.New("container not found")

	// ErrAmbiguousID indicates multiple containers match the given ID prefix.
	ErrAmbiguousID = errors.New("multiple containers match prefix")

	// ErrShortIDTooShort indicates the ID prefix is shorter than the minimum required length.
	ErrShortIDTooShort = errors.New("container ID prefix must be at least 3 characters")
)

// Container state errors
var (
	// ErrContainerRunning indicates the operation cannot be performed because the container is running.
	ErrContainerRunning = errors.New("container is running")

	// ErrContainerStopped indicates the operation cannot be performed because the container is stopped.
	ErrContainerStopped = errors.New("container is not running")

	// ErrContainerExists indicates a container with the same ID already exists.
	ErrContainerExists = errors.New("container already exists")
)

// Configuration errors
var (
	// ErrInvalidConfig indicates the container configuration is invalid.
	ErrInvalidConfig = errors.New("invalid container configuration")

	// ErrMissingRootfs indicates the rootfs path is required but not provided.
	ErrMissingRootfs = errors.New("rootfs path is required")

	// ErrInvalidRootfs indicates the rootfs path is invalid (does not exist or is not a directory).
	ErrInvalidRootfs = errors.New("invalid rootfs path")
)

// Process errors
var (
	// ErrProcessNotFound indicates the container process no longer exists.
	ErrProcessNotFound = errors.New("container process not found")

	// ErrInvalidPID indicates an invalid process ID.
	ErrInvalidPID = errors.New("invalid process ID")
)
