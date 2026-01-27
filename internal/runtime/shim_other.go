//go:build !linux
// +build !linux

package runtime

import (
	"fmt"
	"os"
	"runtime"
)

// RunContainerShim is not supported on non-Linux platforms.
func RunContainerShim() {
	fmt.Fprintf(os.Stderr, "minidocker only supports Linux (current OS: %s)\n", runtime.GOOS)
	os.Exit(1)
}
