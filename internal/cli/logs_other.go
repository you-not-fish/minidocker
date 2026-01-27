//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [OPTIONS] CONTAINER",
	Short: "获取容器的日志",
	Long:  `获取容器的日志。仅支持 Linux 平台。`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker logs only supports Linux (current OS: %s)", runtime.GOOS)
	},
}
