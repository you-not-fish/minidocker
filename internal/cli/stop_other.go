//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var stopTimeout int

var stopCmd = &cobra.Command{
	Use:   "stop CONTAINER [CONTAINER...]",
	Short: "停止运行中的容器",
	Long:  "停止一个或多个运行中的容器。（仅支持 Linux）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
	},
}

func init() {
	stopCmd.Flags().IntVarP(&stopTimeout, "time", "t", 10, "等待容器停止的秒数")
}
