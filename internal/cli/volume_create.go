//go:build linux
// +build linux

package cli

import (
	"fmt"

	"minidocker/internal/state"
	"minidocker/internal/volume"

	"github.com/spf13/cobra"
)

var volumeCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "创建卷",
	Long: `创建一个命名卷。

卷名必须是字母数字字符，可以包含连字符和下划线，长度为 1-64 个字符。
卷数据存储在 /var/lib/minidocker/volumes/<name>/_data/ 目录下。

示例:
  minidocker volume create myvolume
  minidocker volume create my-data-volume
  minidocker volume create app_logs`,
	Args: cobra.ExactArgs(1),
	RunE: createVolume,
}

func createVolume(cmd *cobra.Command, args []string) error {
	volumeName := args[0]

	// 初始化状态存储（获取 rootDir）
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	// 初始化卷存储
	volumeStore, err := volume.NewVolumeStore(store.RootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize volume store: %w", err)
	}

	// 创建卷
	vol, err := volumeStore.Create(volumeName)
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	// 输出卷名（与 Docker 行为一致）
	fmt.Println(vol.Name)
	return nil
}
