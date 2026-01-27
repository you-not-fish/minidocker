//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var killSignal string

var killCmd = &cobra.Command{
	Use:   "kill CONTAINER [CONTAINER...]",
	Short: "杀死运行中的容器",
	Long:  "向一个或多个容器发送信号（默认 SIGKILL）。（仅支持 Linux）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
	},
}

func init() {
	killCmd.Flags().StringVarP(&killSignal, "signal", "s", "KILL", "发送的信号")
}
