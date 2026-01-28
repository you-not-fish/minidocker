//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"

	"minidocker/internal/runtime"
	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var (
	// exec command flags
	execTTY         bool
	execInteractive bool
	// Reserved for future phases
	// execDetach  bool     // Phase 5+: run exec in background
	// execUser    string   // Phase 11: --user flag
	// execWorkdir string   // Phase 11: --workdir flag
	// execEnv     []string // Phase 11: --env flag
)

var execCmd = &cobra.Command{
	Use:   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
	Short: "在运行中的容器内执行命令",
	Long: `在运行中的容器内执行命令。

命令将在与容器 init 进程相同的命名空间中运行。

示例:
  minidocker exec mycontainer /bin/ls
  minidocker exec -it mycontainer /bin/sh
  minidocker exec mycontainer /bin/sh -c "echo hello"`,
	Args: cobra.MinimumNArgs(2),
	RunE: execContainer,
}

func init() {
	execCmd.Flags().BoolVarP(&execTTY, "tty", "t", false, "分配伪终端")
	execCmd.Flags().BoolVarP(&execInteractive, "interactive", "i", false, "保持 STDIN 打开")
	// Reserved flags for future phases (Phase 11)
	// execCmd.Flags().BoolVarP(&execDetach, "detach", "d", false, "后台运行")
	// execCmd.Flags().StringVarP(&execUser, "user", "u", "", "用户名或 UID")
	// execCmd.Flags().StringVarP(&execWorkdir, "workdir", "w", "", "工作目录")
	// execCmd.Flags().StringArrayVarP(&execEnv, "env", "e", nil, "设置环境变量")
}

func execContainer(cmd *cobra.Command, args []string) error {
	containerIDOrName := args[0]
	execCommand := args[1:]

	// 初始化状态存储
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	// 通过 ID/前缀查找容器
	containerState, err := store.Get(containerIDOrName)
	if err != nil {
		return fmt.Errorf("container %s not found: %w", containerIDOrName, err)
	}

	// 验证容器正在运行
	if !containerState.IsRunning() {
		return fmt.Errorf("container %s is not running", containerState.ID[:12])
	}

	// 构建 exec 配置
	config := &runtime.ExecConfig{
		ContainerID:  containerState.ID,
		ContainerPID: containerState.Pid,
		Command:      execCommand,
		TTY:          execTTY,
		Interactive:  execInteractive,
		// User:      execUser,    // Phase 11
		// WorkDir:   execWorkdir, // Phase 11
		// Env:       execEnv,     // Phase 11
	}

	// 执行命令
	exitCode, err := runtime.Exec(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	os.Exit(exitCode)
	return nil // unreachable
}
