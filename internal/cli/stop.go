//go:build linux
// +build linux

package cli

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var stopTimeout int

var stopCmd = &cobra.Command{
	Use:   "stop CONTAINER [CONTAINER...]",
	Short: "停止运行中的容器",
	Long: `停止一个或多个运行中的容器。

先发送 SIGTERM 信号，等待优雅退出。
如果超时后容器仍在运行，则发送 SIGKILL 强制终止。

示例:
  minidocker stop my_container
  minidocker stop -t 30 my_container
  minidocker stop container1 container2`,
	Args: cobra.MinimumNArgs(1),
	RunE: stopContainers,
}

func init() {
	stopCmd.Flags().IntVarP(&stopTimeout, "time", "t", 10, "等待容器停止的秒数")
}

func stopContainers(cmd *cobra.Command, args []string) error {
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	hasError := false
	for _, idOrPrefix := range args {
		if err := stopContainer(store, idOrPrefix, stopTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping %s: %v\n", idOrPrefix, err)
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

func stopContainer(store *state.Store, idOrPrefix string, timeout int) error {
	containerState, err := store.Get(idOrPrefix)
	if err != nil {
		return err
	}

	// 检查容器是否已停止
	if !containerState.IsRunning() {
		// 已停止，幂等成功
		return nil
	}

	pid := containerState.Pid

	// 发送 SIGTERM
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			// 进程不存在，自动修正状态
			containerState.SetStopped(0)
			return nil
		}
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// 等待进程退出
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for time.Now().Before(deadline) {
		// 检查进程是否还存在
		if err := syscall.Kill(pid, 0); err != nil {
			if err == syscall.ESRCH {
				// 进程已退出，重新加载状态
				containerState.Reload()
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 超时，发送 SIGKILL
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			// 在发送 SIGKILL 之前进程已退出
			containerState.Reload()
			return nil
		}
		return fmt.Errorf("failed to send SIGKILL: %w", err)
	}

	// 等待 SIGKILL 生效
	time.Sleep(100 * time.Millisecond)
	containerState.Reload()

	return nil
}
