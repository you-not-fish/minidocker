//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect [OPTIONS] CONTAINER [CONTAINER...]",
	Short: "显示容器的详细信息",
	Long:  `显示容器的详细信息。仅支持 Linux 平台。`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker inspect only supports Linux (current OS: %s)", runtime.GOOS)
	},
}
