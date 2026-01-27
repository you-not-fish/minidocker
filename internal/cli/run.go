package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"minidocker/internal/runtime"

	"github.com/spf13/cobra"
)

var (
	// Run 命令标志
	tty         bool
	interactive bool
	rootfs      string // Phase 2 新增
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
	// NOTE: Phase 1 暂不实现 PTY 分配/终端控制。保留 `-t/-i` 形态用于减少后续
	// Phase 5（exec -it / 真实 TTY）引入时的 CLI 破坏性变更。
	runCmd.Flags().BoolVarP(&tty, "tty", "t", false, "TTY 模式（预留：Phase 1 不分配 PTY）")
	runCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "保持 STDIN 打开（预留：Phase 1 默认已透传 STDIN）")

	// Phase 2 新增：rootfs 参数
	runCmd.Flags().StringVar(&rootfs, "rootfs", "", "容器根文件系统路径（例如：busybox 解压目录）")
}

func runContainer(cmd *cobra.Command, args []string) error {
	// Phase 2: rootfs 路径验证（在父进程中验证，避免子进程启动失败）
	if rootfs != "" {
		// 转换为绝对路径（避免 chdir 后路径错乱）
		absRootfs, err := filepath.Abs(rootfs)
		if err != nil {
			return fmt.Errorf("invalid rootfs path: %w", err)
		}

		// 验证 rootfs 存在且可访问
		if info, err := os.Stat(absRootfs); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("rootfs does not exist: %s", absRootfs)
			}
			return fmt.Errorf("cannot access rootfs: %w", err)
		} else if !info.IsDir() {
			return fmt.Errorf("rootfs is not a directory: %s", absRootfs)
		}

		rootfs = absRootfs
	}

	config := &runtime.ContainerConfig{
		Command: args[0:1],
		Args:    args[1:],
		// Phase 1: 记录 `-t` 但不分配 PTY（见 docs/phase1-dev-notes.md）。
		TTY:    tty,
		Rootfs: rootfs, // Phase 2 新增
	}

	// 生成容器 ID（12位十六进制，用作默认主机名）
	config.ID = runtime.GenerateContainerID()
	config.Hostname = config.ID[:12]

	exitCode, err := runtime.Run(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	os.Exit(exitCode)
	return nil // unreachable
}
