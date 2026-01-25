package cli

import (
	"fmt"
	"os"

	"minidocker/internal/runtime"

	"github.com/spf13/cobra"
)

var (
	// Run 命令标志
	tty         bool
	interactive bool
)

var runCmd = &cobra.Command{
	Use:   "run [flags] COMMAND [ARG...]",
	Short: "在新容器中运行命令",
	Long: `使用指定命令创建并运行一个新容器。

容器将使用 Linux namespaces 进行隔离：
  - UTS namespace (主机名隔离)
  - PID namespace (进程隔离)
  - Mount namespace (文件系统隔离)
  - IPC namespace (进程间通信隔离)

示例:
  minidocker run /bin/sh
  minidocker run -it /bin/bash
  minidocker run /bin/echo "Hello from container"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runContainer,
}

func init() {
	runCmd.Flags().BoolVarP(&tty, "tty", "t", false, "分配伪终端 (pseudo-TTY)")
	runCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "保持 STDIN 打开")
}

func runContainer(cmd *cobra.Command, args []string) error {
	config := &runtime.ContainerConfig{
		Command: args[0:1],
		Args:    args[1:],
		TTY:     tty && interactive, // -it 必须一起使用以启用完整的交互模式
	}

	// 生成容器 ID（12位十六进制，用作默认主机名）
	config.ID = runtime.GenerateContainerID()
	config.Hostname = config.ID[:12]

	if err := runtime.Run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	return nil
}
