package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	// 版本信息
	Version = "0.1.0"

	// 全局标志
	// rootDir 是容器状态根目录
	// 默认值：$MINIDOCKER_ROOT 环境变量，或 /var/lib/minidocker
	rootDir string
)

var rootCmd = &cobra.Command{
	Use:   "minidocker",
	Short: "用于学习的最小化容器运行时",
	Long: `MiniDocker 是一个为帮助理解 Docker 底层原理而设计的
简化版容器运行时实现。

它支持基本的容器操作，包括：
  - 使用 Linux namespaces 创建隔离进程
  - 在容器中运行命令
  - (未来) 使用 cgroups 进行资源限制
  - (未来) 用于镜像的 Overlay 文件系统
  - (未来) 容器网络`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       Version,
}

// Execute 运行根命令
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// 添加子命令
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(stopCmd)    // Phase 3 新增
	rootCmd.AddCommand(killCmd)    // Phase 3 新增
	rootCmd.AddCommand(rmCmd)      // Phase 3 新增
	rootCmd.AddCommand(psCmd)      // Phase 4 新增
	rootCmd.AddCommand(logsCmd)    // Phase 4 新增
	rootCmd.AddCommand(inspectCmd) // Phase 4 新增
	rootCmd.AddCommand(execCmd)    // Phase 5 新增
	rootCmd.AddCommand(imagesCmd)  // Phase 8 新增
	rootCmd.AddCommand(rmiCmd)     // Phase 8 新增
	rootCmd.AddCommand(loadCmd)    // Phase 8 新增
	rootCmd.AddCommand(volumeCmd)  // Phase 10 新增

	// Phase 3: 全局标志
	rootCmd.PersistentFlags().StringVar(&rootDir, "root", "",
		"容器状态根目录（默认: $MINIDOCKER_ROOT 或 /var/lib/minidocker）")
}
