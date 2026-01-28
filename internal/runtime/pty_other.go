//go:build !linux
// +build !linux

package runtime

import (
	"fmt"
	"os/exec"
	"runtime"
)

// execWithPTY 在 TTY 模式下执行命令
func execWithPTY(cmd *exec.Cmd, config *ExecConfig) (int, error) {
	return -1, fmt.Errorf("PTY exec only supports Linux (current OS: %s)", runtime.GOOS)
}
