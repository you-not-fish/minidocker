//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps [OPTIONS]",
	Short: "列出容器",
	Long:  `列出容器。仅支持 Linux 平台。`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker ps only supports Linux (current OS: %s)", runtime.GOOS)
	},
}
