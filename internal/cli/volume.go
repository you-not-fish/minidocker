//go:build linux
// +build linux

package cli

import (
	"github.com/spf13/cobra"
)

var volumeCmd = &cobra.Command{
	Use:   "volume",
	Short: "管理卷",
	Long: `管理 minidocker 卷。

卷可用于在容器之间持久化和共享数据。
命名卷由 minidocker 管理，数据存储在 /var/lib/minidocker/volumes/ 目录下。

示例:
  minidocker volume create myvolume
  minidocker volume ls
  minidocker volume rm myvolume`,
}

func init() {
	// 添加子命令
	volumeCmd.AddCommand(volumeCreateCmd)
	volumeCmd.AddCommand(volumeLsCmd)
	volumeCmd.AddCommand(volumeRmCmd)
}
