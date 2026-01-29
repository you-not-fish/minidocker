//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"minidocker/internal/cgroups"
	"minidocker/internal/network"
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
// Phase 7 更新：
// - 配置网络（bridge/host/none 模式）
// - 容器退出后清理网络资源
//
// This aligns with the industry "per-container shim" model (e.g. containerd-shim).
func RunContainerShim() {
	containerDir := os.Getenv(envutil.StatePathEnvVar)
	notify := openShimNotifyWriter()

	// Phase 6: cgroup 相关变量
	var cgroupManager cgroups.Manager
	var cgroupPath string

	// Phase 7: network 相关变量
	var networkManager network.Manager
	var networkState *network.NetworkState
	var containerID string // 用于清理时引用

	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if notify != nil {
			fmt.Fprintf(notify, "ERR: %s\n", msg)
			notify.Close()
		}
		// Phase 7: 清理网络
		if networkManager != nil && networkState != nil && containerID != "" {
			_ = networkManager.Teardown(containerID, networkState)
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
	containerID = cfg.ID // 设置 containerID 用于清理

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

	// Phase 7: 从配置中恢复网络配置
	if cfg.NetworkMode != "" {
		rCfg.NetworkConfig = &network.NetworkConfig{
			Mode: network.NetworkMode(cfg.NetworkMode),
		}
		if len(cfg.PortMappings) > 0 {
			rCfg.NetworkConfig.PortMappings = make([]network.PortMapping, len(cfg.PortMappings))
			for i, pm := range cfg.PortMappings {
				rCfg.NetworkConfig.PortMappings[i] = network.PortMapping{
					HostIP:        pm.HostIP,
					HostPort:      pm.HostPort,
					ContainerPort: pm.ContainerPort,
					Protocol:      pm.Protocol,
				}
			}
		}

		// 在 state.json 中至少持久化网络模式（包括 host/none）。
		// bridge 模式会在 Setup 后填充 IP/veth/portMappings 等详细信息。
		st.NetworkState = &state.NetworkState{
			Mode: cfg.NetworkMode,
		}

		// 初始化网络管理器并确保 bridge 存在
		if rCfg.NetworkConfig.NeedsNetworkNamespace() {
			// 获取 rootDir（从 containerDir 向上两级）
			rootDir := filepath.Dir(filepath.Dir(containerDir))
			networkManager, err = network.NewManager(rootDir)
			if err != nil {
				fail("initialize network manager: %v", err)
			}

			// 确保 bridge 存在（对于 bridge 模式）
			if rCfg.NetworkConfig.GetMode() == network.NetworkModeBridge {
				if err := networkManager.EnsureBridge(rCfg.NetworkConfig); err != nil {
					fail("ensure bridge: %v", err)
				}
			}
		}
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

	// Phase 7: 配置网络（需要 PID 来移动 veth 到容器网络命名空间）
	if networkManager != nil && rCfg.NetworkConfig != nil {
		networkState, err = networkManager.Setup(cfg.ID, rCfg.NetworkConfig, cmd.Process.Pid)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			logs.Close()
			fail("setup network: %v", err)
		}

		// 保存网络状态到容器状态
		st.NetworkState = &state.NetworkState{
			Mode:          string(networkState.Mode),
			IPAddress:     networkState.IPAddress,
			Gateway:       networkState.Gateway,
			MacAddress:    networkState.MacAddress,
			VethHost:      networkState.VethHost,
			VethContainer: networkState.VethContainer,
		}
		if len(networkState.PortMappings) > 0 {
			st.NetworkState.PortMappings = make([]state.PortMapping, len(networkState.PortMappings))
			for i, pm := range networkState.PortMappings {
				st.NetworkState.PortMappings[i] = state.PortMapping{
					HostIP:        pm.HostIP,
					HostPort:      pm.HostPort,
					ContainerPort: pm.ContainerPort,
					Protocol:      pm.Protocol,
				}
			}
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

	// Phase 7: 清理网络（先于 cgroup）
	if networkManager != nil && networkState != nil {
		_ = networkManager.Teardown(cfg.ID, networkState)
	}

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
