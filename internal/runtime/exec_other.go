//go:build !linux
// +build !linux

package runtime

import (
	"fmt"
	"runtime"
)

// ExecConfig 保存 exec 命令的配置
type ExecConfig struct {
	ContainerID  string
	ContainerPID int
	Command      []string
	TTY          bool
	Interactive  bool
}

// Exec 在运行中容器的命名空间内执行命令
func Exec(config *ExecConfig) (int, error) {
	return -1, fmt.Errorf("exec only supports Linux (current OS: %s)", runtime.GOOS)
}

// RunExecInit 是 exec 进程在 re-exec 后的入口点
func RunExecInit() {
	panic(fmt.Sprintf("exec init only supports Linux (current OS: %s)", runtime.GOOS))
}
