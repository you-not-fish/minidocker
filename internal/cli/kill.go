//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var killSignal string

var killCmd = &cobra.Command{
	Use:   "kill CONTAINER [CONTAINER...]",
	Short: "杀死运行中的容器",
	Long: `向一个或多个容器发送信号（默认 SIGKILL）。

支持的信号名称：
  SIGKILL, KILL, 9
  SIGTERM, TERM, 15
  SIGHUP, HUP, 1
  SIGINT, INT, 2
  SIGQUIT, QUIT, 3
  SIGUSR1, USR1, 10
  SIGUSR2, USR2, 12

示例:
  minidocker kill my_container
  minidocker kill -s SIGTERM my_container
  minidocker kill -s 15 my_container`,
	Args: cobra.MinimumNArgs(1),
	RunE: killContainers,
}

func init() {
	killCmd.Flags().StringVarP(&killSignal, "signal", "s", "KILL", "发送的信号")
}

// signalMap 定义信号名称到 syscall.Signal 的映射
var signalMap = map[string]syscall.Signal{
	"SIGKILL": syscall.SIGKILL,
	"KILL":    syscall.SIGKILL,
	"9":       syscall.SIGKILL,

	"SIGTERM": syscall.SIGTERM,
	"TERM":    syscall.SIGTERM,
	"15":      syscall.SIGTERM,

	"SIGHUP": syscall.SIGHUP,
	"HUP":    syscall.SIGHUP,
	"1":      syscall.SIGHUP,

	"SIGINT": syscall.SIGINT,
	"INT":    syscall.SIGINT,
	"2":      syscall.SIGINT,

	"SIGQUIT": syscall.SIGQUIT,
	"QUIT":    syscall.SIGQUIT,
	"3":       syscall.SIGQUIT,

	"SIGUSR1": syscall.SIGUSR1,
	"USR1":    syscall.SIGUSR1,
	"10":      syscall.SIGUSR1,

	"SIGUSR2": syscall.SIGUSR2,
	"USR2":    syscall.SIGUSR2,
	"12":      syscall.SIGUSR2,
}

// parseSignal 解析信号名称或数字到 syscall.Signal
func parseSignal(sigStr string) (syscall.Signal, error) {
	sigStr = strings.ToUpper(strings.TrimSpace(sigStr))

	// 先尝试从映射中查找
	if sig, ok := signalMap[sigStr]; ok {
		return sig, nil
	}

	// 尝试解析为数字
	if num, err := strconv.Atoi(sigStr); err == nil {
		if num > 0 && num < 32 {
			return syscall.Signal(num), nil
		}
		return 0, fmt.Errorf("invalid signal number: %d", num)
	}

	return 0, fmt.Errorf("unknown signal: %s", sigStr)
}

func killContainers(cmd *cobra.Command, args []string) error {
	// 解析信号
	sig, err := parseSignal(killSignal)
	if err != nil {
		return err
	}

	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	hasError := false
	for _, idOrPrefix := range args {
		if err := killContainer(store, idOrPrefix, sig); err != nil {
			fmt.Fprintf(os.Stderr, "Error killing %s: %v\n", idOrPrefix, err)
			hasError = true
		} else {
			// 成功时输出容器 ID（与 Docker 行为一致）
			fmt.Println(idOrPrefix)
		}
	}

	if hasError {
		os.Exit(1)
	}
	return nil
}

func killContainer(store *state.Store, idOrPrefix string, sig syscall.Signal) error {
	containerState, err := store.Get(idOrPrefix)
	if err != nil {
		return err
	}

	// 检查容器是否正在运行
	if !containerState.IsRunning() {
		return fmt.Errorf("container %s is not running", idOrPrefix)
	}

	pid := containerState.Pid

	// 发送信号
	if err := syscall.Kill(pid, sig); err != nil {
		if err == syscall.ESRCH {
			// 进程不存在，自动修正状态
			containerState.SetStopped(0)
			return fmt.Errorf("container process not found (state corrected)")
		}
		return fmt.Errorf("failed to send signal: %w", err)
	}

	return nil
}
