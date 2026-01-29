//go:build linux
// +build linux

package runtime

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"minidocker/internal/state"
	"minidocker/pkg/envutil"

	"golang.org/x/sys/unix"
)

// RunOptions 配置容器运行方式
type RunOptions struct {
	// StateStore 是状态存储（必需）
	StateStore *state.Store
}

// logFiles 用于跟踪需要关闭的日志文件
type logFiles struct {
	stdout *os.File
	stderr *os.File
}

func (l *logFiles) Close() {
	if l.stdout != nil {
		l.stdout.Close()
	}
	if l.stderr != nil {
		l.stderr.Close()
	}
}

// Run 使用给定的配置创建并运行一个新容器。
//
// Phase 3 更新：
// - 集成状态管理：创建状态目录、更新状态
// - 支持后台模式（config.Detached）：立即返回，后台等待退出
// - 日志重定向：stdout/stderr 写入日志文件
//
// 注意：这个函数不应该调用 os.Exit。
// 退出码应由 CLI（或后续阶段的 daemon/manager）统一处理。
func Run(config *ContainerConfig, opts *RunOptions) (int, error) {
	if opts == nil || opts.StateStore == nil {
		return -1, fmt.Errorf("RunOptions with StateStore is required")
	}

	// 1. 创建状态目录和初始状态
	stateConfig := &state.ContainerConfig{
		ID:       config.ID,
		Command:  config.Command,
		Args:     config.Args,
		Hostname: config.Hostname,
		Rootfs:   config.Rootfs,
		TTY:      config.TTY,
		Detached: config.Detached,
	}

	containerState, err := opts.StateStore.Create(stateConfig)
	if err != nil {
		return -1, fmt.Errorf("failed to create container state: %w", err)
	}

	// 清理函数：启动失败时删除状态目录
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			opts.StateStore.ForceDelete(config.ID)
		}
	}()

	if config.Detached {
		// 后台模式：启动 per-container shim 进程，并等待其将状态更新为 running。
		// run -d 必须立即返回，但 exitCode/state 的最终更新需要一个持久的父进程（类似 containerd-shim）。
		if err := startDetachedShim(containerState.GetContainerDir()); err != nil {
			return -1, fmt.Errorf("failed to start container shim: %w", err)
		}

		// 启动成功，取消清理
		cleanupOnError = false
		return 0, nil
	}

	// 2. 设置日志文件（前台模式）
	logs, err := setupLogFiles(containerState.GetContainerDir())
	if err != nil {
		return -1, fmt.Errorf("failed to setup log files: %w", err)
	}

	// 3. 创建父进程
	cmd, err := newParentProcess(config, containerState.GetContainerDir(), logs)
	if err != nil {
		logs.Close()
		return -1, fmt.Errorf("failed to create parent process: %w", err)
	}

	// 4. 启动子进程
	if err := cmd.Start(); err != nil {
		logs.Close()
		return -1, fmt.Errorf("failed to start container process: %w", err)
	}

	// 5. 更新状态为 running
	if err := containerState.SetRunning(cmd.Process.Pid); err != nil {
		// 启动成功但状态更新失败，尝试杀死进程
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logs.Close()
		return -1, fmt.Errorf("failed to update container state: %w", err)
	}

	// 启动成功，取消清理
	cleanupOnError = false

	// 前台模式：等待退出
	exitCode := waitForExit(cmd)
	containerState.SetStopped(exitCode)
	logs.Close()

	return exitCode, nil
}

// startDetachedShim starts a per-container shim process and waits for a single-line
// status message from it ("OK" or "ERR: ...").
func startDetachedShim(containerDir string) error {
	notifyR, notifyW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create shim notify pipe: %w", err)
	}
	defer notifyR.Close()

	shimCmd := exec.Command("/proc/self/exe")
	shimCmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // detach from controlling terminal
	}

	// Do NOT inherit stdio: otherwise `minidocker run -d` invoked via CombinedOutput()
	// would hang if the shim keeps the parent's stdout/stderr pipes open.
	shimCmd.Stdin = nil
	shimCmd.Stdout = nil
	shimCmd.Stderr = nil

	// Pass container directory + notify fd.
	shimCmd.Env = append(os.Environ(),
		envutil.ShimEnvVar+"=1",
		envutil.StatePathEnvVar+"="+containerDir,
		envutil.ShimNotifyFdEnvVar+"=3",
	)
	shimCmd.ExtraFiles = []*os.File{notifyW} // fd=3 in child

	if err := shimCmd.Start(); err != nil {
		_ = notifyW.Close()
		return fmt.Errorf("start shim process: %w", err)
	}
	_ = notifyW.Close()

	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r := bufio.NewReader(notifyR)
		line, err := r.ReadString('\n')
		ch <- result{line: strings.TrimSpace(line), err: err}
	}()

	select {
	case res := <-ch:
		// Success path: shim reported OK
		if res.err == nil && res.line == "OK" {
			_ = shimCmd.Process.Release()
			return nil
		}

		// shim reported an error message
		if strings.HasPrefix(res.line, "ERR:") {
			_ = shimCmd.Wait() // best-effort (should exit quickly on ERR)
			return fmt.Errorf("%s", strings.TrimSpace(res.line))
		}

		// Unexpected/EOF
		_ = shimCmd.Wait() // best-effort
		if res.err != nil {
			return fmt.Errorf("shim failed to report status: %w", res.err)
		}
		return fmt.Errorf("shim failed to report status: %q", res.line)

	case <-time.After(5 * time.Second):
		// Avoid hanging forever if shim is stuck before reporting readiness.
		_ = shimCmd.Process.Kill()
		_ = shimCmd.Wait()
		return fmt.Errorf("timeout waiting for container shim to start")
	}
}

// setupLogFiles 创建日志文件
func setupLogFiles(containerDir string) (*logFiles, error) {
	logDir := filepath.Join(containerDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("create stdout log: %w", err)
	}

	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		stdout.Close()
		return nil, fmt.Errorf("create stderr log: %w", err)
	}

	return &logFiles{stdout: stdout, stderr: stderr}, nil
}

// waitForExit 等待进程退出并返回退出码
func waitForExit(cmd *exec.Cmd) int {
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return -1
	}
	return 0
}

// newParentProcess 创建一个新命令，该命令将在启用命名空间隔离的情况下
// 重新执行当前二进制文件。
//
// 重新执行模式是必要的，因为：
//  1. Go 的运行时在 main() 运行之前会生成多个线程
//  2. 在 Go 中直接在当前进程内做会影响整个进程/线程组的 namespace 操作需要非常谨慎，
//     否则容易受到运行时多线程的影响而产生难以定位的问题
//  3. 通过 re-exec，子进程从一开始就处在目标 namespace 中，并进入明确的 init(PID1) 路径，
//     组织方式更贴近 runc
func newParentProcess(config *ContainerConfig, containerDir string, logs *logFiles) (*exec.Cmd, error) {
	// 重新执行当前二进制文件
	// /proc/self/exe 始终指向当前可执行文件
	cmd := exec.Command("/proc/self/exe")

	// 使用克隆标志配置命名空间隔离
	// 这些标志告诉内核为子进程创建新的命名空间
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | // 新的 UTS namespace (主机名)
			syscall.CLONE_NEWPID | // 新的 PID namespace (进程 ID)
			syscall.CLONE_NEWNS | // 新的 Mount namespace (文件系统挂载)
			syscall.CLONE_NEWIPC, // 新的 IPC namespace (System V IPC, POSIX 消息队列)
		// 注意：CLONE_NEWNET 未包含在第1阶段中。
		// 网络命名空间将在第7阶段 (feat-network-bridge) 中添加。
		// 目前，容器共享主机网络。

		// 注意：CLONE_NEWUSER 未包含在第1阶段中。
		// 用户命名空间将在第16阶段 (feat-rootless) 中添加。
	}

	// 后台模式：创建新会话，脱离控制终端
	if config.Detached {
		cmd.SysProcAttr.Setsid = true
	}

	// 为 init 进程设置环境
	cmd.Env = append(os.Environ(),
		envutil.InitEnvVar+"=1",
		envutil.StatePathEnvVar+"="+containerDir,
	)

	// 设置标准输入输出
	if config.Detached {
		// 后台模式：关闭 stdin，重定向 stdout/stderr 到日志文件
		cmd.Stdin = nil
		cmd.Stdout = logs.stdout
		cmd.Stderr = logs.stderr
	} else if config.TTY {
		// TTY 模式：直接连接到终端
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		// 非 TTY 前台模式：透传 stdin，同时写入终端和日志文件
		cmd.Stdin = os.Stdin
		cmd.Stdout = newTeeWriter(os.Stdout, logs.stdout)
		cmd.Stderr = newTeeWriter(os.Stderr, logs.stderr)
	}

	return cmd, nil
}

// teeWriter 同时写入多个 Writer
type teeWriter struct {
	primary *os.File
	extra   *os.File
}

// newTeeWriter 创建一个同时写入两个目标的 Writer
func newTeeWriter(primary, extra *os.File) *teeWriter {
	return &teeWriter{primary: primary, extra: extra}
}

func (t *teeWriter) Write(p []byte) (n int, err error) {
	// 写入主目标
	n, err = t.primary.Write(p)
	if err != nil {
		return n, err
	}

	// 写入额外目标（忽略错误）
	t.extra.Write(p)

	return n, nil
}

// GetContainerPID 返回容器 init 进程的 PID。
// 这供以后的阶段用于 exec、stop 等。
// 注意：这必须在 cmd.Start() 之后但在 cmd.Wait() 之前调用。
func GetContainerPID(cmd *exec.Cmd) int {
	if cmd.Process != nil {
		return cmd.Process.Pid
	}
	return 0
}

// setMountPropagation 将挂载传播设置为私有。
// 这可以防止容器内的挂载传播到主机。
func setMountPropagation() error {
	// 将所有挂载设为私有以防止传播到主机
	// 这相当于：mount --make-rprivate /
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to set mount propagation to private: %w", err)
	}
	return nil
}
