//go:build linux
// +build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var (
	// inspect 命令标志
	inspectFormat string
)

var inspectCmd = &cobra.Command{
	Use:   "inspect [OPTIONS] CONTAINER [CONTAINER...]",
	Short: "显示容器的详细信息",
	Long: `显示一个或多个容器的详细信息。

输出 JSON 格式的容器配置和状态信息。

注意：--format 参数当前仅支持默认 JSON 输出，Go 模板格式化功能预留到后续版本。

示例:
  minidocker inspect my_container
  minidocker inspect container1 container2`,
	Args: cobra.MinimumNArgs(1),
	RunE: inspectContainers,
}

func init() {
	// --format 预留接口，Phase 4 简化实现只支持默认 JSON
	inspectCmd.Flags().StringVarP(&inspectFormat, "format", "f", "",
		"格式化输出（预留：当前仅支持默认 JSON 输出）")
}

// InspectOutput 表示 inspect 命令的完整输出
type InspectOutput struct {
	ID         string         `json:"Id"`
	Created    time.Time      `json:"Created"`
	State      StateInfo      `json:"State"`
	Config     ConfigInfo     `json:"Config"`
	HostConfig HostConfigInfo `json:"HostConfig"`
	LogPath    string         `json:"LogPath"`
}

// StateInfo 表示容器状态信息
type StateInfo struct {
	Status     string     `json:"Status"`
	Running    bool       `json:"Running"`
	Pid        int        `json:"Pid"`
	ExitCode   int        `json:"ExitCode"`
	StartedAt  *time.Time `json:"StartedAt,omitempty"`
	FinishedAt *time.Time `json:"FinishedAt,omitempty"`
}

// ConfigInfo 表示容器配置信息
type ConfigInfo struct {
	Hostname string   `json:"Hostname"`
	Tty      bool     `json:"Tty"`
	Cmd      []string `json:"Cmd"`
	Detached bool     `json:"Detached"`
}

// HostConfigInfo 表示主机配置信息
type HostConfigInfo struct {
	Rootfs string `json:"Rootfs"`
}

func inspectContainers(cmd *cobra.Command, args []string) error {
	// 如果指定了 --format 但不是空的，警告用户
	if inspectFormat != "" {
		fmt.Fprintf(os.Stderr, "Warning: --format is reserved for future use, currently only default JSON output is supported\n")
	}

	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	outputs := make([]InspectOutput, 0, len(args))
	hasError := false

	for _, idOrPrefix := range args {
		output, err := inspectContainer(store, idOrPrefix)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error inspecting %s: %v\n", idOrPrefix, err)
			hasError = true
			continue
		}
		outputs = append(outputs, *output)
	}

	// 输出 JSON
	if len(outputs) > 0 {
		data, err := json.MarshalIndent(outputs, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(data))
	}

	if hasError {
		os.Exit(1)
	}

	return nil
}

func inspectContainer(store *state.Store, idOrPrefix string) (*InspectOutput, error) {
	containerState, err := store.Get(idOrPrefix)
	if err != nil {
		return nil, err
	}

	// 加载配置
	config, err := state.LoadConfig(containerState.GetContainerDir())
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// 构建完整命令
	fullCmd := config.GetCommand()

	// 计算退出码
	exitCode := 0
	if containerState.ExitCode != nil {
		exitCode = *containerState.ExitCode
	}

	output := &InspectOutput{
		ID:      containerState.ID,
		Created: containerState.CreatedAt,
		State: StateInfo{
			Status:     string(containerState.Status),
			Running:    containerState.Status == state.StatusRunning,
			Pid:        containerState.Pid,
			ExitCode:   exitCode,
			StartedAt:  containerState.StartedAt,
			FinishedAt: containerState.FinishedAt,
		},
		Config: ConfigInfo{
			Hostname: config.Hostname,
			Tty:      config.TTY,
			Cmd:      fullCmd,
			Detached: config.Detached,
		},
		HostConfig: HostConfigInfo{
			Rootfs: config.Rootfs,
		},
		LogPath: containerState.GetLogDir(),
	}

	return output, nil
}
