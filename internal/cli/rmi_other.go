//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var rmiCmd = &cobra.Command{
	Use:   "rmi [OPTIONS] IMAGE [IMAGE...]",
	Short: "删除一个或多个镜像",
	Long:  `删除一个或多个本地镜像。如果镜像被多个标签引用，只删除指定的标签。`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("rmi command is only supported on Linux (current: %s)", runtime.GOOS)
	},
}

func init() {
	rmiCmd.Flags().BoolP("force", "f", false, "强制删除镜像")
}
