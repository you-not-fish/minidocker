//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"minidocker/internal/cgroups"
	"minidocker/internal/state"
	"minidocker/pkg/envutil"
)

// RunContainerShim is the entrypoint for the per-container shim process.
//
// Why a shim?
// `minidocker run -d` must return immediately, but we still need a parent process to:
// - reap the container init process (so we can reliably observe exit)
// - persist the final exit code and stopped state to state.json
//
// Phase 6 更新：
// - 创建 cgroup 并将容器进程加入
// - 容器退出后清理 cgroup
//
// This aligns with the industry "per-container shim" model (e.g. containerd-shim).
func RunContainerShim() {
	containerDir := os.Getenv(envutil.StatePathEnvVar)
	notify := openShimNotifyWriter()

	// Phase 6: cgroup 相关变量
	var cgroupManager cgroups.Manager
	var cgroupPath string

	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if notify != nil {
			fmt.Fprintf(notify, "ERR: %s\n", msg)
			notify.Close()
		}
		// Phase 6: 清理 cgroup
		if cgroupManager != nil && cgroupPath != "" {
			_ = cgroupManager.Destroy(cgroupPath)
		}
		fmt.Fprintf(os.Stderr, "shim: %s\n", msg)
		os.Exit(1)
	}

	if containerDir == "" {
		fail("missing %s environment variable", envutil.StatePathEnvVar)
	}

	// Load config.json (immutable)
	cfg, err := state.LoadConfig(containerDir)
	if err != nil {
		fail("load config: %v", err)
	}

	// Load state.json (mutable)
	st, err := state.LoadState(containerDir)
	if err != nil {
		fail("load state: %v", err)
	}

	// Prepare runtime config for the init process
	rCfg := &ContainerConfig{
		ID:       cfg.ID,
		Command:  cfg.Command,
		Args:     cfg.Args,
		Hostname: cfg.Hostname,
		Rootfs:   cfg.Rootfs,
		TTY:      cfg.TTY,
		Detached: true, // shim only exists for detached containers
	}

	// Phase 6: 从配置中恢复 cgroup 配置
	if cfg.HasCgroupConfig() {
		rCfg.CgroupConfig = &cgroups.CgroupConfig{
			Memory:     cfg.Memory,
			MemorySwap: cfg.MemorySwap,
			CPUQuota:   cfg.CPUQuota,
			CPUPeriod:  cfg.CPUPeriod,
			PidsLimit:  cfg.PidsLimit,
		}

		// 创建 cgroup
		cgroupManager, err = cgroups.NewManager()
		if err != nil {
			fail("initialize cgroup manager: %v", err)
		}

		cgroupPath = cgroups.GetCgroupPath(cfg.ID)
		if err := cgroupManager.Create(cgroupPath, rCfg.CgroupConfig); err != nil {
			fail("create cgroup: %v", err)
		}

		// 更新状态中的 cgroup 路径
		st.CgroupPath = cgroupPath
	}

	// Open log files for the container init
	logs, err := setupLogFiles(containerDir)
	if err != nil {
		fail("setup log files: %v", err)
	}

	// Start the container init process as a child of the shim
	cmd, err := newParentProcess(rCfg, containerDir, logs)
	if err != nil {
		logs.Close()
		fail("create container process: %v", err)
	}

	if err := cmd.Start(); err != nil {
		logs.Close()
		fail("start container process: %v", err)
	}

	// Phase 6: 将进程加入 cgroup
	if cgroupManager != nil && cgroupPath != "" {
		if err := cgroupManager.Apply(cgroupPath, cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			logs.Close()
			fail("apply cgroup: %v", err)
		}
	}

	// Persist running state (must happen before notifying the parent)
	if err := st.SetRunning(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logs.Close()
		fail("update state to running: %v", err)
	}

	// Notify the parent process that the container is running and state is updated.
	if notify != nil {
		_, _ = fmt.Fprintln(notify, "OK")
		_ = notify.Close()
	}

	// Wait for container exit and persist exit code
	exitCode := waitForExit(cmd)
	_ = st.SetStopped(exitCode)
	logs.Close()

	// Phase 6: 清理 cgroup
	if cgroupManager != nil && cgroupPath != "" {
		_ = cgroupManager.Destroy(cgroupPath)
	}

	os.Exit(0)
}

func openShimNotifyWriter() *os.File {
	fdStr := os.Getenv(envutil.ShimNotifyFdEnvVar)
	if strings.TrimSpace(fdStr) == "" {
		return nil
	}

	fd, err := strconv.Atoi(fdStr)
	if err != nil || fd < 3 {
		return nil
	}

	// Note: fd comes from exec.Cmd.ExtraFiles (>= 3).
	return os.NewFile(uintptr(fd), "minidocker-shim-notify")
}
