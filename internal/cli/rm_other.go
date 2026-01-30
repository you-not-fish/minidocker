//go:build !linux
// +build !linux

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	rmForce   bool
	rmVolumes bool
)

var rmCmd = &cobra.Command{
	Use:   "rm CONTAINER [CONTAINER...]",
	Short: "删除容器",
	Long:  "删除一个或多个容器。（仅支持 Linux）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("minidocker only supports Linux (current OS: %s)", runtime.GOOS)
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "强制删除运行中的容器")
	rmCmd.Flags().BoolVarP(&rmVolumes, "volumes", "v", false, "预留：删除关联的卷（当前不实现，命名卷默认持久化）")
}
