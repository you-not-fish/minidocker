//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var imagesCmd = &cobra.Command{
	Use:   "images [OPTIONS]",
	Short: "列出本地镜像",
	Long:  `列出本地存储的所有容器镜像。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("images command is only supported on Linux (current: %s)", runtime.GOOS)
	},
}

func init() {
	imagesCmd.Flags().BoolP("quiet", "q", false, "只显示镜像 ID")
	imagesCmd.Flags().Bool("no-trunc", false, "不截断输出")
	imagesCmd.Flags().String("format", "table", "输出格式 (table/json)")
}
