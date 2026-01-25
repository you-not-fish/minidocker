//go:build !linux
// +build !linux

package runtime

import (
	"fmt"
	"os"
	"runtime"
)

// 环境变量名称（必须与 init.go 匹配）
const initEnvVar = "MINIDOCKER_INIT"
const configEnvVar = "MINIDOCKER_CONFIG"

// RunContainerInit 在非 Linux 平台上不受支持。
func RunContainerInit() {
	fmt.Fprintf(os.Stderr, "minidocker init is only supported on Linux (current OS: %s)\n", runtime.GOOS)
	os.Exit(1)
}
