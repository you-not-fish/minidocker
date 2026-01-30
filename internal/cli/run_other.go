//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// Run 命令标志
	tty         bool
	interactive bool
	rootfs      string
	detach      bool

	// Phase 11 新增
	containerName string
	hostname      string
	envVars       []string
	workDir       string
	user          string
)

var runCmd = &cobra.Command{
	Use:   "run [flags] COMMAND [ARG...]",
	Short: "在新容器中运行命令",
	Long:  "使用指定命令创建并运行一个新容器。（仅支持 Linux）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
	},
}

func init() {
	runCmd.Flags().BoolVarP(&tty, "tty", "t", false, "TTY 模式")
	runCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "保持 STDIN 打开")
	runCmd.Flags().StringVar(&rootfs, "rootfs", "", "容器根文件系统路径")
	runCmd.Flags().BoolVarP(&detach, "detach", "d", false, "后台运行容器")

	// Phase 11 新增
	runCmd.Flags().StringVar(&containerName, "name", "", "容器名称")
	runCmd.Flags().StringVar(&hostname, "hostname", "", "容器主机名")
	runCmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "设置环境变量")
	runCmd.Flags().StringVarP(&workDir, "workdir", "w", "", "容器内工作目录")
	runCmd.Flags().StringVarP(&user, "user", "u", "", "运行用户")
}
