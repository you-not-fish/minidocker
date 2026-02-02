//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// pullCmd is the `minidocker pull` command (stub for non-Linux).
var pullCmd = &cobra.Command{
	Use:   "pull [OPTIONS] IMAGE",
	Short: "从远端仓库拉取镜像",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("pull is only supported on Linux (current: %s)", runtime.GOOS)
	},
}

func init() {
	pullCmd.Flags().BoolP("quiet", "q", false, "静默模式，仅输出镜像 ID")
	pullCmd.Flags().String("platform", "linux/amd64", "目标平台 (os/arch)")
}
