//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minidocker/internal/state"
	"minidocker/internal/volume"

	"github.com/spf13/cobra"
)

var volumeRmCmd = &cobra.Command{
	Use:   "rm NAME [NAME...]",
	Short: "删除卷",
	Long: `删除一个或多个命名卷。

注意：删除卷会永久删除卷中的所有数据。
如果卷正在被使用，删除会失败（未来可能支持 -f 强制删除）。

示例:
  minidocker volume rm myvolume
  minidocker volume rm vol1 vol2 vol3`,
	Aliases: []string{"remove"},
	Args:    cobra.MinimumNArgs(1),
	RunE:    removeVolumes,
}

func removeVolumes(cmd *cobra.Command, args []string) error {
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

	hasError := false
	for _, name := range args {
		// Safety: refuse to delete a volume referenced by any container (running or stopped),
		// matching Docker's behavior.
		inUse, usedBy, err := isVolumeInUse(store.RootDir, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking volume usage %s: %v\n", name, err)
			hasError = true
			continue
		}
		if inUse {
			fmt.Fprintf(os.Stderr, "Error removing volume %s: volume is in use by container(s): %s\n", name, strings.Join(usedBy, ","))
			hasError = true
			continue
		}

		if err := volumeStore.Delete(name); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing volume %s: %v\n", name, err)
			hasError = true
		} else {
			// 成功时输出卷名（与 Docker 行为一致）
			fmt.Println(name)
		}
	}

	if hasError {
		os.Exit(1)
	}
	return nil
}

func isVolumeInUse(rootDir, volumeName string) (bool, []string, error) {
	containersDir := filepath.Join(rootDir, "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("read containers directory: %w", err)
	}

	var usedBy []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		containerDir := filepath.Join(containersDir, e.Name())
		cfg, err := state.LoadConfig(containerDir)
		if err != nil {
			// Best-effort: skip broken container dirs (consistent with other commands).
			continue
		}
		for _, m := range cfg.Mounts {
			if m.Type == string(volume.MountTypeVolume) && m.Source == volumeName {
				usedBy = append(usedBy, cfg.ShortID())
				break
			}
		}
	}

	if len(usedBy) > 0 {
		return true, usedBy, nil
	}
	return false, nil, nil
}
