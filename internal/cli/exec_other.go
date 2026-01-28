//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	execTTY         bool
	execInteractive bool
)

var execCmd = &cobra.Command{
	Use:   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
	Short: "在运行中的容器内执行命令",
	Long:  `在运行中的容器内执行命令。仅支持 Linux 平台。`,
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker exec only supports Linux (current OS: %s)", runtime.GOOS)
	},
}

func init() {
	execCmd.Flags().BoolVarP(&execTTY, "tty", "t", false, "分配伪终端")
	execCmd.Flags().BoolVarP(&execInteractive, "interactive", "i", false, "保持 STDIN 打开")
}
