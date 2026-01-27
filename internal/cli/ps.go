//go:build linux
// +build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var (
	// ps 命令标志
	psAll     bool
	psQuiet   bool
	psFormat  string
	psNoTrunc bool
)

var psCmd = &cobra.Command{
	Use:   "ps [OPTIONS]",
	Short: "列出容器",
	Long: `列出容器。

默认只显示运行中的容器。使用 -a 显示所有容器。

示例:
  minidocker ps           # 列出运行中的容器
  minidocker ps -a        # 列出所有容器
  minidocker ps -q        # 只显示容器 ID
  minidocker ps --format json  # JSON 格式输出`,
	Args: cobra.NoArgs,
	RunE: listContainers,
}

func init() {
	psCmd.Flags().BoolVarP(&psAll, "all", "a", false, "显示所有容器（默认只显示运行中）")
	psCmd.Flags().BoolVarP(&psQuiet, "quiet", "q", false, "只显示容器 ID")
	psCmd.Flags().StringVar(&psFormat, "format", "table", "格式化输出（table/json）")
	psCmd.Flags().BoolVar(&psNoTrunc, "no-trunc", false, "不截断输出")
}

// PsEntry 表示 ps 命令的单行输出
type PsEntry struct {
	ID       string    `json:"Id"`
	Status   string    `json:"Status"`
	Created  time.Time `json:"Created"`
	Command  string    `json:"Command"`
	Pid      int       `json:"Pid,omitempty"`
	ExitCode *int      `json:"ExitCode,omitempty"`
}

func listContainers(cmd *cobra.Command, args []string) error {
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	states, err := store.List(psAll)
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// 转换为 PsEntry
	entries := make([]PsEntry, 0, len(states))
	for _, s := range states {
		// 加载配置获取命令信息
		config, err := state.LoadConfig(s.GetContainerDir())
		if err != nil {
			// 如果配置加载失败，跳过该容器
			continue
		}

		command := strings.Join(config.GetCommand(), " ")
		entry := PsEntry{
			ID:       s.ID,
			Status:   string(s.Status),
			Created:  s.CreatedAt,
			Command:  command,
			Pid:      s.Pid,
			ExitCode: s.ExitCode,
		}
		entries = append(entries, entry)
	}

	// 对齐常见容器 CLI 行为：按创建时间倒序输出
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Created.After(entries[j].Created)
	})

	// 根据格式输出
	switch psFormat {
	case "json":
		return outputJSON(entries)
	case "table":
		return outputTable(entries)
	default:
		return fmt.Errorf("unknown format: %s (supported: table, json)", psFormat)
	}
}

func outputJSON(entries []PsEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func outputTable(entries []PsEntry) error {
	if psQuiet {
		// 只输出 ID
		for _, entry := range entries {
			if psNoTrunc {
				fmt.Println(entry.ID)
			} else {
				fmt.Println(shortID(entry.ID))
			}
		}
		return nil
	}

	// 使用 tabwriter 格式化表格输出
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tSTATUS\tCREATED\tCOMMAND")

	for _, entry := range entries {
		id := entry.ID
		if !psNoTrunc {
			id = shortID(id)
		}

		command := entry.Command
		if !psNoTrunc && len(command) > 30 {
			command = command[:27] + "..."
		}

		created := formatCreatedTime(entry.Created)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, entry.Status, created, command)
	}

	return w.Flush()
}

// shortID 返回容器 ID 的前 12 个字符
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// formatCreatedTime 格式化创建时间
func formatCreatedTime(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Minute {
		return "Less than a minute ago"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else if duration < 7*24*time.Hour {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	} else if duration < 30*24*time.Hour {
		weeks := int(duration.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	} else {
		return t.Format("2006-01-02 15:04:05")
	}
}
