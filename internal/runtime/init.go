//go:build linux
// +build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"minidocker/internal/state"

	"golang.org/x/sys/unix"
)

// RunContainerInit 是容器 init 进程（PID 1）的入口点。
// 当二进制文件检测到 MINIDOCKER_INIT=1 环境变量时调用此函数。
//
// 作为容器中的 PID 1，此进程具有特殊责任：
// 1. 回收僵尸进程 - 当任何子进程退出时，init 必须对其进行 wait()
// 2. 转发信号 - 像 SIGTERM 这样的信号应该转发给主子进程
// 3. 以主子进程的退出代码退出
//
// 此设计与 tini/dumb-init 的行为一致。
func RunContainerInit() {
	// 从环境获取容器配置
	config, err := getConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: failed to get config: %v\n", err)
		os.Exit(1)
	}

	// 设置容器环境
	if err := setupContainerEnvironment(config); err != nil {
		fmt.Fprintf(os.Stderr, "init: setup failed: %v\n", err)
		os.Exit(1)
	}

	// 运行用户命令并处理信号
	exitCode := runUserCommand(config)
	os.Exit(exitCode)
}

// getConfig returns the container config for init(PID1).
//
// Phase 3 improvement: prefer loading persisted `config.json` from the container "bundle" directory
// (passed via MINIDOCKER_STATE_PATH), instead of passing a potentially large JSON blob via env vars.
//
// Backward compatibility: if MINIDOCKER_CONFIG is present, it is still accepted.
func getConfig() (*ContainerConfig, error) {
	// Backward-compatible env JSON (Phase 1/2)
	if configJSON := os.Getenv(configEnvVar); strings.TrimSpace(configJSON) != "" {
		var cfg ContainerConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", configEnvVar, err)
		}
		return &cfg, nil
	}

	// Preferred: load config.json from bundle/container dir.
	containerDir := os.Getenv(statePathEnvVar)
	if strings.TrimSpace(containerDir) == "" {
		return nil, fmt.Errorf("missing %s environment variable", statePathEnvVar)
	}

	cfg, err := state.LoadConfig(containerDir)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", containerDir, err)
	}

	return &ContainerConfig{
		ID:       cfg.ID,
		Command:  cfg.Command,
		Args:     cfg.Args,
		Hostname: cfg.Hostname,
		TTY:      cfg.TTY,
		Rootfs:   cfg.Rootfs,
		Detached: cfg.Detached,
	}, nil
}

// setupContainerEnvironment 配置容器环境。
// 这将在命名空间隔离到位后调用。
func setupContainerEnvironment(config *ContainerConfig) error {
	// Phase 2 关键调整：setupRootfs() 必须在所有其他操作之前执行！
	// 原因：pivot_root 会改变根目录，影响后续所有路径操作
	if err := setupRootfs(config); err != nil {
		return fmt.Errorf("setup rootfs: %w", err)
	}

	// 1. 设置主机名（UTS namespace 必须被隔离）
	hostname := config.GetHostname()
	if err := unix.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("failed to set hostname to %q: %w", hostname, err)
	}

	// 2. 将挂载传播设置为私有
	// 这可以防止挂载传播到主机
	if err := setMountPropagation(); err != nil {
		return err
	}

	// 3. Phase 2 移除旧的 mountProc() 调用
	// 因为 setupRootfs() 已经在 pivot_root 后正确挂载了 /proc

	// 未来扩展点（第2阶段+）：
	// - setupCgroups(config)  // 第6阶段: cgroup 资源限制
	// - setupNetwork(config)  // 第7阶段: 网络配置
	// - setupMounts(config)   // 第10阶段: 卷挂载

	return nil
}

// mountProc 已迁移到 rootfs.go（Phase 2）
// 保留此注释以标记历史变更

// runUserCommand 执行用户命令并处理信号转发 + 僵尸进程回收。
// 返回用户命令的退出代码。
func runUserCommand(config *ContainerConfig) int {
	// 构建命令
	cmdArgs := config.GetCommand()
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "init: no command specified")
		return 1
	}

	// 创建命令
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 设置环境（对于第11阶段，我们将在此添加自定义环境变量）
	cmd.Env = os.Environ()

	// 清除 MINIDOCKER_* 环境变量，以防泄露到容器中
	var filteredEnv []string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, initEnvVar+"=") {
			continue
		}
		if strings.HasPrefix(env, configEnvVar+"=") {
			continue
		}
		// Phase 3 新增：过滤状态目录路径环境变量
		if strings.HasPrefix(env, statePathEnvVar+"=") {
			continue
		}
		filteredEnv = append(filteredEnv, env)
	}
	cmd.Env = filteredEnv

	// 设置信号处理（并在其中启动用户命令）
	// PID 1 必须能转发信号并回收僵尸进程
	return handleSignalsAndWait(cmd)
}

// handleSignalsAndWait 负责：
// - 启动主子进程（用户命令）
// - SIGCHLD：回收僵尸进程（包括孙进程）
// - SIGTERM/SIGINT/SIGHUP/SIGQUIT：转发给主子进程
//
// 关键点：必须在启动主子进程前安装 signal.Notify，否则主子进程“秒退”时可能丢 SIGCHLD，
// 从而导致 init 阻塞等待信号（假死）。
func handleSignalsAndWait(cmd *exec.Cmd) int {
	// 用于接收信号的通道
	sigChan := make(chan os.Signal, 10)

	// 注册所有应转发或处理的信号
	signal.Notify(sigChan,
		syscall.SIGCHLD, // 子进程状态改变
		syscall.SIGTERM, // 终止请求
		syscall.SIGINT,  // 中断 (Ctrl+C)
		syscall.SIGHUP,  // 挂起
		syscall.SIGQUIT, // 退出
		syscall.SIGUSR1, // 用户定义信号 1
		syscall.SIGUSR2, // 用户定义信号 2
	)
	defer signal.Stop(sigChan)

	// 启动用户命令（必须在 signal.Notify 之后）
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "init: failed to start command: %v\n", err)
		return 1
	}

	// 跟踪主子进程
	mainChildPid := cmd.Process.Pid
	var mainChildExitCode int
	mainChildExited := false

	// 处理“主子进程极快退出”的情况：即使还没收到 SIGCHLD，也先做一次非阻塞回收。
	if exitCode, childExited := reapZombies(mainChildPid); childExited {
		return exitCode
	}

	// 主循环：等待信号并处理它们
	for {
		sig := <-sigChan

		switch sig {
		case syscall.SIGCHLD:
			// 子进程状态改变（退出、停止等）
			// 我们需要回收所有僵尸进程，而不仅仅是主子进程
			exitCode, childExited := reapZombies(mainChildPid)
			if childExited {
				mainChildExitCode = exitCode
				mainChildExited = true
			}

			// 如果主子进程已退出，我们也可以退出
			if mainChildExited {
				return mainChildExitCode
			}

		case syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT:
			// 转发终止信号给主子进程
			if cmd.Process != nil && !mainChildExited {
				_ = cmd.Process.Signal(sig)
			}

		case syscall.SIGUSR1, syscall.SIGUSR2:
			// 转发用户定义信号给主子进程
			if cmd.Process != nil && !mainChildExited {
				_ = cmd.Process.Signal(sig)
			}
		}
	}
}

// reapZombies 等待任何僵尸子进程，并在主子进程退出时返回退出代码。
// 返回 (exitCode, wasMainChild)。
func reapZombies(mainChildPid int) (int, bool) {
	mainChildExitCode := 0
	mainChildExited := false

	for {
		// 等待任何子进程，非阻塞
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)

		if err != nil {
			// ECHILD 意味着没有更多子进程需要等待
			if err == unix.ECHILD {
				break
			}
			// 其他错误是意外的，但不应导致 init 崩溃
			break
		}

		if pid <= 0 {
			// 没有更多处于可等待状态的子进程
			break
		}

		// 检查这是否是主子进程
		if pid == mainChildPid {
			mainChildExited = true
			if status.Exited() {
				mainChildExitCode = status.ExitStatus()
			} else if status.Signaled() {
				// 进程被信号杀死
				// 惯例：退出代码 = 128 + 信号编号
				mainChildExitCode = 128 + int(status.Signal())
			}
		}
		// 对于其他子进程（孤儿孙进程），我们只是默默地回收它们
	}

	return mainChildExitCode, mainChildExited
}
