//go:build !linux
// +build !linux

package runtime

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Run 在非 Linux 平台上不受支持。
// 容器依赖于 Linux 特有的特性，如 namespaces 和 cgroups。
func Run(config *ContainerConfig) (int, error) {
	return -1, fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
}

// newParentProcess 在非 Linux 平台上不受支持。
func newParentProcess(config *ContainerConfig) (*exec.Cmd, error) {
	return nil, fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
}

// GetContainerPID 在非 Linux 平台上不受支持。
func GetContainerPID(cmd *exec.Cmd) int {
	return 0
}

// setMountPropagation 在非 Linux 平台上不受支持。
func setMountPropagation() error {
	return fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
}
