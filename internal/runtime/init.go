//go:build linux
// +build linux

package runtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"minidocker/internal/state"
	"minidocker/internal/volume"
	"minidocker/pkg/envutil"

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
	if configJSON := os.Getenv(envutil.ConfigEnvVar); strings.TrimSpace(configJSON) != "" {
		var cfg ContainerConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", envutil.ConfigEnvVar, err)
		}
		return &cfg, nil
	}

	// Preferred: load config.json from bundle/container dir.
	containerDir := os.Getenv(envutil.StatePathEnvVar)
	if strings.TrimSpace(containerDir) == "" {
		return nil, fmt.Errorf("missing %s environment variable", envutil.StatePathEnvVar)
	}

	cfg, err := state.LoadConfig(containerDir)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", containerDir, err)
	}

	config := &ContainerConfig{
		ID:         cfg.ID,
		Command:    cfg.Command,
		Args:       cfg.Args,
		Hostname:   cfg.Hostname,
		TTY:        cfg.TTY,
		Rootfs:     cfg.Rootfs,
		Detached:   cfg.Detached,
		Name:       cfg.Name,       // Phase 11
		Env:        cfg.Env,        // Phase 11
		WorkingDir: cfg.WorkingDir, // Phase 11
		User:       cfg.User,       // Phase 11
	}

	// Phase 10: 加载挂载配置
	if len(cfg.Mounts) > 0 {
		config.Mounts = make([]volume.Mount, len(cfg.Mounts))
		for i, m := range cfg.Mounts {
			config.Mounts[i] = volume.Mount{
				Type:       volume.MountType(m.Type),
				Source:     m.Source,
				Target:     m.Target,
				ReadOnly:   m.ReadOnly,
				VolumePath: m.VolumePath,
			}
		}
	}

	return config, nil
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

	// Phase 10: 卷挂载
	// - 当存在 rootfs（会 pivot_root）时：挂载在 setupRootfs() 中、pivot_root 之前完成（对齐 runc）
	// - 当没有 rootfs（Phase 1 兼容）时：直接挂到当前 "/" 下的目标路径
	if config.Rootfs == "" && len(config.Mounts) > 0 {
		if err := setupMounts("", config.Mounts); err != nil {
			return fmt.Errorf("setup mounts: %w", err)
		}
	}

	// 未来扩展点：
	// - setupCgroups(config)  // 第6阶段: cgroup 资源限制（在父进程中处理）
	// - setupNetwork(config)  // 第7阶段: 网络配置（在父进程中处理）

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

	// Phase 11: 切换用户（必须在 exec 前完成）
	if config.User != "" {
		if err := switchUser(config.User, config.Rootfs); err != nil {
			fmt.Fprintf(os.Stderr, "init: switch user: %v\n", err)
			return 1
		}
	}

	// Phase 11: 切换工作目录
	if config.WorkingDir != "" {
		if err := os.Chdir(config.WorkingDir); err != nil {
			fmt.Fprintf(os.Stderr, "init: chdir to %s: %v\n", config.WorkingDir, err)
			return 1
		}
	}

	// 创建命令
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Phase 11: 设置环境变量
	// 1. 从基础环境开始（过滤掉 MINIDOCKER_* 变量）
	// 2. 合并用户指定的环境变量（覆盖同名变量）
	baseEnv := envutil.FilterMinidockerEnv(os.Environ())
	cmd.Env = mergeEnvVars(baseEnv, config.Env)

	// 设置信号处理（并在其中启动用户命令）
	// PID 1 必须能转发信号并回收僵尸进程
	return handleSignalsAndWait(cmd)
}

// switchUser 切换运行用户
// 支持格式:
//   - user: 用户名或 UID
//   - user:group: 用户名/UID 和 组名/GID
//
// 注意: 必须在 exec 前调用，因为 setuid/setgid 只影响当前进程
//
// 安全关键：调用顺序必须是 setgroups → setgid → setuid
// 原因：一旦 setuid 降权后，进程可能失去修改 groups/gid 的权限
func switchUser(userSpec, rootfs string) error {
	uid, gid, err := parseUserSpec(userSpec, rootfs)
	if err != nil {
		return err
	}

	// 1. 首先设置 supplementary groups（必须在 setuid 之前）
	// 使用只包含目标 GID 的列表，清空其他 supplementary groups
	// 安全关键：如果失败必须中止，否则可能保留原 supplementary groups（如 root 组）
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups([%d]): %w", gid, err)
	}

	// 2. 设置 GID（必须在 setuid 之前）
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d): %w", gid, err)
	}

	// 3. 最后设置 UID（降权操作，之后无法再修改 groups/gid）
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid(%d): %w", uid, err)
	}

	return nil
}

// parseUserSpec 解析用户规格
// 支持格式:
//   - "1000" -> uid=1000, gid=1000
//   - "1000:1000" -> uid=1000, gid=1000
//   - "nobody" -> 从 /etc/passwd 解析
//   - "nobody:nogroup" -> 从 /etc/passwd 和 /etc/group 解析
func parseUserSpec(spec, rootfs string) (uid, gid int, err error) {
	parts := strings.SplitN(spec, ":", 2)
	userPart := parts[0]
	groupPart := ""
	if len(parts) > 1 {
		groupPart = parts[1]
	}

	// 解析用户
	uid, gid, err = lookupUser(userPart, rootfs)
	if err != nil {
		return 0, 0, fmt.Errorf("lookup user %q: %w", userPart, err)
	}

	// 如果指定了组，覆盖 gid
	if groupPart != "" {
		gid, err = lookupGroup(groupPart, rootfs)
		if err != nil {
			return 0, 0, fmt.Errorf("lookup group %q: %w", groupPart, err)
		}
	}

	return uid, gid, nil
}

// lookupUser 查找用户 UID 和 GID
// 如果是数字，直接解析；否则从 /etc/passwd 查找
func lookupUser(name, rootfs string) (uid, gid int, err error) {
	// 尝试解析为数字
	if id, err := parseID(name); err == nil {
		return id, id, nil // 默认 gid = uid
	}

	// 从 /etc/passwd 查找
	passwdPath := "/etc/passwd"
	if rootfs != "" {
		// 在 pivot_root 之前，需要使用 rootfs 路径
		// 但在 pivot_root 之后，直接使用 /etc/passwd
	}

	file, err := os.Open(passwdPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", passwdPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		if fields[0] == name {
			uid, err := parseID(fields[2])
			if err != nil {
				return 0, 0, fmt.Errorf("parse uid %q: %w", fields[2], err)
			}
			gid, err := parseID(fields[3])
			if err != nil {
				return 0, 0, fmt.Errorf("parse gid %q: %w", fields[3], err)
			}
			return uid, gid, nil
		}
	}

	return 0, 0, fmt.Errorf("user %q not found in %s", name, passwdPath)
}

// lookupGroup 查找组 GID
// 如果是数字，直接解析；否则从 /etc/group 查找
func lookupGroup(name, rootfs string) (gid int, err error) {
	// 尝试解析为数字
	if id, err := parseID(name); err == nil {
		return id, nil
	}

	// 从 /etc/group 查找
	groupPath := "/etc/group"

	file, err := os.Open(groupPath)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", groupPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		if fields[0] == name {
			gid, err := parseID(fields[2])
			if err != nil {
				return 0, fmt.Errorf("parse gid %q: %w", fields[2], err)
			}
			return gid, nil
		}
	}

	return 0, fmt.Errorf("group %q not found in %s", name, groupPath)
}

// parseID 解析数字 ID
func parseID(s string) (int, error) {
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if id < 0 {
		return 0, fmt.Errorf("id must be non-negative")
	}
	return id, nil
}

// mergeEnvVars 合并环境变量，后者覆盖前者
func mergeEnvVars(base, override []string) []string {
	envMap := make(map[string]string)

	// 解析基础环境
	for _, env := range base {
		if idx := strings.Index(env, "="); idx != -1 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	// 覆盖/添加用户指定的环境变量
	for _, env := range override {
		if idx := strings.Index(env, "="); idx != -1 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	// 转换回切片
	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}

	return result
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

// setupMounts 执行卷挂载。
//
// 当 rootfs != "" 时，挂载目标会被映射到 rootfs 下（例如 rootfs + "/data"），
// 以便后续 pivot_root 后在容器内表现为 "/data"。
// 这能确保 mount(2) 的 source（宿主路径）在 pivot_root 前仍可解析（对齐 runc 的做法）。
func setupMounts(rootfs string, mounts []volume.Mount) error {
	if rootfs != "" {
		abs, err := filepath.Abs(rootfs)
		if err != nil {
			return fmt.Errorf("abs rootfs: %w", err)
		}
		rootfs = abs
	}
	for _, m := range mounts {
		if err := performMount(rootfs, m); err != nil {
			return fmt.Errorf("mount %s -> %s: %w", m.Source, m.Target, err)
		}
	}
	return nil
}

// performMount 执行单个挂载
func performMount(rootfs string, m volume.Mount) error {
	source, err := resolveMountSource(m)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("empty mount source for target %s", m.Target)
	}

	target := m.Target
	if rootfs != "" {
		// target is an absolute container path; map it under rootfs for pre-pivot mounting.
		target = filepath.Join(rootfs, strings.TrimPrefix(m.Target, "/"))
	}

	// Ensure mount target exists and matches source type (file vs dir).
	isDir, err := ensureMountTarget(source, target)
	if err != nil {
		return err
	}

	// Perform bind mount.
	flags := uintptr(unix.MS_BIND)
	if isDir {
		flags |= uintptr(unix.MS_REC)
	}
	if err := unix.Mount(source, target, "", flags, ""); err != nil {
		return fmt.Errorf("bind mount %s -> %s: %w", source, target, err)
	}

	// Read-only bind mounts require a remount with MS_RDONLY|MS_REMOUNT.
	if m.ReadOnly {
		remountFlags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY)
		if isDir {
			remountFlags |= uintptr(unix.MS_REC)
		}
		if err := unix.Mount("", target, "", remountFlags, ""); err != nil {
			return fmt.Errorf("remount %s as read-only: %w", target, err)
		}
	}

	return nil
}

func ensureMountTarget(source, target string) (bool, error) {
	srcInfo, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("stat mount source %s: %w", source, err)
	}

	if srcInfo.IsDir() {
		if err := os.MkdirAll(target, 0755); err != nil {
			return false, fmt.Errorf("create mount target dir %s: %w", target, err)
		}
		return true, nil
	}

	// Source is a file: ensure the parent dir exists and create an empty target file if needed.
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return false, fmt.Errorf("create mount target parent dir %s: %w", filepath.Dir(target), err)
	}
	if fi, err := os.Stat(target); err == nil {
		if fi.IsDir() {
			return false, fmt.Errorf("mount target %s is a directory, but source %s is a file", target, source)
		}
		return false, nil
	}
	f, err := os.OpenFile(target, os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("create mount target file %s: %w", target, err)
	}
	_ = f.Close()
	return false, nil
}

func resolveMountSource(m volume.Mount) (string, error) {
	switch m.Type {
	case volume.MountTypeBind:
		return m.Source, nil
	case volume.MountTypeVolume:
		// Prefer VolumePath if present; otherwise resolve via volume store using MINIDOCKER_STATE_PATH.
		if strings.TrimSpace(m.VolumePath) != "" {
			return m.VolumePath, nil
		}
		return resolveNamedVolumePath(m.Source)
	default:
		return "", fmt.Errorf("unknown mount type: %s", m.Type)
	}
}

func resolveNamedVolumePath(name string) (string, error) {
	containerDir := os.Getenv(envutil.StatePathEnvVar)
	if strings.TrimSpace(containerDir) == "" {
		return "", fmt.Errorf("missing %s environment variable (cannot resolve named volume %q)", envutil.StatePathEnvVar, name)
	}

	// containerDir is <rootDir>/containers/<id>
	rootDir := filepath.Dir(filepath.Dir(containerDir))

	vs, err := volume.NewVolumeStore(rootDir)
	if err != nil {
		return "", fmt.Errorf("initialize volume store: %w", err)
	}

	// Auto-create if not exists (Docker-like behavior).
	if !vs.Exists(name) {
		if _, err := vs.Create(name); err != nil {
			// A concurrent creator may have won; re-check exists.
			if !vs.Exists(name) {
				return "", fmt.Errorf("create volume %q: %w", name, err)
			}
		}
	}

	vol, err := vs.Get(name)
	if err != nil {
		return "", fmt.Errorf("get volume %q: %w", name, err)
	}

	return vol.Path, nil
}
