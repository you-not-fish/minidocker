//go:build linux
// +build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	goruntime "runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// 用于触发 exec 模式的环境变量
const execEnvVar = "MINIDOCKER_EXEC"

// 用于传递 exec 配置的环境变量
const execConfigEnvVar = "MINIDOCKER_EXEC_CONFIG"

// ExecConfig 保存 exec 命令的配置
type ExecConfig struct {
	ContainerID  string   `json:"container_id"`
	ContainerPID int      `json:"container_pid"`
	Command      []string `json:"command"`
	TTY          bool     `json:"tty"`
	Interactive  bool     `json:"interactive"`
	// 预留给 Phase 11
	// User    string   `json:"user,omitempty"`
	// WorkDir string   `json:"workdir,omitempty"`
	// Env     []string `json:"env,omitempty"`
}

// Exec 在运行中容器的命名空间内执行命令
func Exec(config *ExecConfig) (int, error) {
	if config.ContainerPID <= 0 {
		return -1, fmt.Errorf("invalid container PID: %d", config.ContainerPID)
	}

	// 验证容器进程存在
	if err := syscall.Kill(config.ContainerPID, 0); err != nil {
		return -1, fmt.Errorf("container process %d not found: %w", config.ContainerPID, err)
	}

	// 序列化配置用于 re-exec
	configJSON, err := json.Marshal(config)
	if err != nil {
		return -1, fmt.Errorf("marshal exec config: %w", err)
	}

	// Re-exec 自身以加入命名空间
	cmd := exec.Command("/proc/self/exe")
	cmd.Env = append(os.Environ(),
		execEnvVar+"=1",
		execConfigEnvVar+"="+string(configJSON),
	)

	// 处理 PTY 模式
	if config.TTY {
		return execWithPTY(cmd, config)
	}

	// 非 PTY 模式：直接透传 stdio
	if config.Interactive {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal()), nil
			}
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// RunExecInit 是 exec 进程在 re-exec 后的入口点。
// 当检测到 MINIDOCKER_EXEC=1 时调用。
func RunExecInit() {
	// setns() 只影响当前 OS 线程；Go 调度可能在不同线程间切换 goroutine。
	// 为确保 joinNamespaces() 与后续 fork/exec 发生在同一线程上，必须锁线程。
	goruntime.LockOSThread()

	configJSON := os.Getenv(execConfigEnvVar)
	if configJSON == "" {
		fmt.Fprintf(os.Stderr, "exec init: missing config\n")
		os.Exit(1)
	}

	var config ExecConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		fmt.Fprintf(os.Stderr, "exec init: parse config: %v\n", err)
		os.Exit(1)
	}

	// 加入容器命名空间
	if err := joinNamespaces(config.ContainerPID); err != nil {
		fmt.Fprintf(os.Stderr, "exec init: join namespaces: %v\n", err)
		os.Exit(1)
	}

	// 在 TTY 模式下，控制字符（如 Ctrl+C）会通过 PTY 触发 SIGINT 发往前台进程组。
	// 当前进程是 exec init 包装器；我们让它忽略 SIGINT，避免被误杀而导致子进程脱离/残留。
	// 子进程仍会收到该信号。
	if config.TTY {
		signal.Ignore(syscall.SIGINT)
	}

	// 切换到容器根目录
	if err := unix.Chdir("/"); err != nil {
		fmt.Fprintf(os.Stderr, "exec init: chdir: %v\n", err)
		os.Exit(1)
	}

	// 执行命令
	exitCode := runExecCommand(&config)
	os.Exit(exitCode)
}

// joinNamespaces 使用 setns() 加入指定容器的命名空间
func joinNamespaces(pid int) error {
	// 命名空间类型列表（顺序重要：mnt 应该最后）
	// 原因：加入 mnt 命名空间后路径解析会改变
	namespaces := []struct {
		name string
		flag int
	}{
		{"ipc", unix.CLONE_NEWIPC},
		{"uts", unix.CLONE_NEWUTS},
		{"pid", unix.CLONE_NEWPID},
		{"mnt", unix.CLONE_NEWNS},
		// 注意：CLONE_NEWNET 将在 Phase 7 添加
		// 注意：CLONE_NEWUSER 将在 Phase 16 添加
	}

	for _, ns := range namespaces {
		nsPath := fmt.Sprintf("/proc/%d/ns/%s", pid, ns.name)
		fd, err := unix.Open(nsPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open %s namespace (%s): %w", ns.name, nsPath, err)
		}

		if err := unix.Setns(fd, ns.flag); err != nil {
			unix.Close(fd)
			return fmt.Errorf("setns %s: %w", ns.name, err)
		}
		unix.Close(fd)
	}

	return nil
}

// runExecCommand 在加入命名空间后执行用户命令
func runExecCommand(config *ExecConfig) int {
	if len(config.Command) == 0 {
		fmt.Fprintln(os.Stderr, "exec init: no command specified")
		return 1
	}

	// 查找可执行文件
	binary, err := exec.LookPath(config.Command[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "exec init: command not found: %s\n", config.Command[0])
		return 127
	}

	// 过滤 MINIDOCKER_* 环境变量
	env := filterExecEnv(os.Environ())

	// 注意：PID namespace 通过 setns() 加入后，只对“后续创建的子进程”生效，
	// 因此这里必须 fork/exec，而不能直接 syscall.Exec() 替换自身。
	cmd := exec.Command(binary, config.Command[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Go 的 ExitCode() 在被信号杀死时可能返回 -1；这里统一转换为 shell 惯例 128+signal。
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return exitErr.ExitCode()
		}
		// 启动失败（例如 EACCES）按惯例返回 126
		fmt.Fprintf(os.Stderr, "exec init: exec %s: %v\n", binary, err)
		return 126
	}

	return 0
}

// filterExecEnv 移除 MINIDOCKER_* 环境变量
func filterExecEnv(env []string) []string {
	var filtered []string
	for _, e := range env {
		if !isExecMinidockerEnv(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// isExecMinidockerEnv 检查环境变量是否是 MINIDOCKER_* 变量
func isExecMinidockerEnv(env string) bool {
	prefixes := []string{
		execEnvVar + "=",
		execConfigEnvVar + "=",
		initEnvVar + "=",
		configEnvVar + "=",
		statePathEnvVar + "=",
		shimEnvVar + "=",
		shimNotifyFdEnvVar + "=",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(env, prefix) {
			return true
		}
	}
	return false
}
