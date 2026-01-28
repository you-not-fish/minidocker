package main

import (
	"os"

	"minidocker/internal/cli"
	"minidocker/internal/runtime"
)

func main() {
	// 检查这是否是容器内的 init 进程。
	// 我们使用环境变量来检测这一点，而不是子命令
	// 以避免污染用户的命令命名空间。
	if os.Getenv("MINIDOCKER_INIT") == "1" {
		runtime.RunContainerInit()
		return
	}

	// Phase 3: per-container shim for detached containers.
	// This process stays alive to reap the container init and persist exit code/state.
	if os.Getenv("MINIDOCKER_SHIM") == "1" {
		runtime.RunContainerShim()
		return
	}

	// Phase 5: exec init process (namespace joining)
	// This process joins an existing container's namespaces and executes a command.
	if os.Getenv("MINIDOCKER_EXEC") == "1" {
		runtime.RunExecInit()
		return
	}

	cli.Execute()
}
