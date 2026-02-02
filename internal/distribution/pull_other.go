//go:build !linux
// +build !linux

package distribution

import (
	"fmt"
	"io"
	"runtime"

	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/opencontainers/go-digest"

	"minidocker/internal/image"
)

var errNotSupported = fmt.Errorf("distribution operations are only supported on Linux (current: %s)", runtime.GOOS)

// PullOptions configures the pull operation.
type PullOptions struct {
	Quiet    bool
	Platform *v1.Platform
	Output   io.Writer
}

// DefaultPullOptions returns the default pull options.
func DefaultPullOptions() *PullOptions {
	return &PullOptions{}
}

// Pull is not supported on non-Linux platforms.
func Pull(ref string, store image.Store, opts *PullOptions) (digest.Digest, error) {
	return "", errNotSupported
}
