//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var loadCmd = &cobra.Command{
	Use:   "load [OPTIONS] -i FILE",
	Short: "从 OCI tar 归档导入镜像",
	Long: `从 OCI tar 归档导入镜像到本地存储。

支持的格式：
  - OCI Image Layout tar 归档（由 buildah, skopeo 等工具创建）

示例：
  minidocker load -i alpine.tar
  minidocker load -i alpine.tar -t alpine:latest`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("load command is only supported on Linux (current: %s)", runtime.GOOS)
	},
}

func init() {
	loadCmd.Flags().StringP("input", "i", "", "要导入的 tar 归档文件路径（必需）")
	loadCmd.Flags().StringP("tag", "t", "", "为导入的镜像添加标签（可选）")
}
