//go:build linux
// +build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"minidocker/internal/state"
	"minidocker/internal/volume"

	"github.com/spf13/cobra"
)

var (
	volumeLsQuiet  bool
	volumeLsFormat string
)

var volumeLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出卷",
	Long: `列出所有命名卷。

默认以表格格式输出，包含卷名、驱动和挂载点。
使用 -q 只输出卷名。
使用 --format json 输出 JSON 格式。

示例:
  minidocker volume ls
  minidocker volume ls -q
  minidocker volume ls --format json`,
	Aliases: []string{"list"},
	RunE:    listVolumes,
}

func init() {
	volumeLsCmd.Flags().BoolVarP(&volumeLsQuiet, "quiet", "q", false, "只显示卷名")
	volumeLsCmd.Flags().StringVar(&volumeLsFormat, "format", "table", "输出格式（table/json）")
}

func listVolumes(cmd *cobra.Command, args []string) error {
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

	// 获取所有卷
	volumes, err := volumeStore.List()
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	// 按名称排序
	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].Name < volumes[j].Name
	})

	// 输出
	if volumeLsQuiet {
		for _, v := range volumes {
			fmt.Println(v.Name)
		}
		return nil
	}

	switch volumeLsFormat {
	case "json":
		return outputVolumesJSON(volumes)
	case "table":
		return outputVolumesTable(volumes)
	default:
		return fmt.Errorf("unsupported format: %s (supported: table, json)", volumeLsFormat)
	}
}

func outputVolumesTable(volumes []*volume.VolumeInfo) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()

	// 表头
	fmt.Fprintln(w, "VOLUME NAME\tDRIVER\tMOUNTPOINT")

	// 数据行
	for _, v := range volumes {
		fmt.Fprintf(w, "%s\t%s\t%s\n", v.Name, v.Driver, v.Path)
	}

	return nil
}

func outputVolumesJSON(volumes []*volume.VolumeInfo) error {
	data, err := json.MarshalIndent(volumes, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal volumes: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
