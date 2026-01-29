//go:build linux
// +build linux

package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"minidocker/internal/cgroups"
	"minidocker/internal/network"
	"minidocker/internal/runtime"
	"minidocker/internal/state"

	"github.com/spf13/cobra"
)

var (
	// Run 命令标志
	tty         bool
	interactive bool
	rootfs      string // Phase 2 新增
	detach      bool   // Phase 3 新增：后台运行
	// name     string // Phase 11 实现：容器名称

	// Phase 6 新增：资源限制
	memoryLimit string // -m, --memory，如 "512m", "1g"
	memorySwap  string // --memory-swap
	cpus        string // --cpus，如 "0.5", "2"
	cpuQuota    int64  // --cpu-quota（高级）
	cpuPeriod   int64  // --cpu-period（高级）
	pidsLimit   int64  // --pids-limit

	// Phase 7 新增：网络配置
	networkMode  string   // --network，如 "bridge", "host", "none"
	publishPorts []string // -p, --publish，如 "8080:80", "8080:80/tcp"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] COMMAND [ARG...]",
	Short: "在新容器中运行命令",
	Long: `使用指定命令创建并运行一个新容器。

容器将使用 Linux namespaces 进行隔离：
  - UTS namespace (主机名隔离)
  - PID namespace (进程隔离)
  - Mount namespace (文件系统隔离)
  - IPC namespace (进程间通信隔离)
  - Network namespace (网络隔离，Phase 7)

资源限制（Phase 6，需要 cgroup v2）：
  - 内存限制: -m, --memory
  - CPU 限制: --cpus
  - 进程数限制: --pids-limit

网络模式（Phase 7）：
  - bridge: 默认模式，创建独立网络命名空间并连接到 minidocker0 bridge
  - host: 共享宿主机网络
  - none: 只有 loopback 的独立网络命名空间

示例:
  minidocker run /bin/sh
  minidocker run -it /bin/bash
  minidocker run /bin/echo "Hello from container"
  minidocker run -d --rootfs /tmp/rootfs /bin/sleep 100
  minidocker run -m 512m --cpus 0.5 --rootfs /tmp/rootfs /bin/sh
  minidocker run --pids-limit 100 --rootfs /tmp/rootfs /bin/sh
  minidocker run --network bridge --rootfs /tmp/rootfs /bin/sh
  minidocker run --network host --rootfs /tmp/rootfs /bin/sh
  minidocker run -p 8080:80 --rootfs /tmp/rootfs /bin/httpd`,
	Args: cobra.MinimumNArgs(1),
	RunE: runContainer,
}

func init() {
	// NOTE: Phase 1 暂不实现 PTY 分配/终端控制。保留 `-t/-i` 形态用于减少后续
	// Phase 5（exec -it / 真实 TTY）引入时的 CLI 破坏性变更。
	runCmd.Flags().BoolVarP(&tty, "tty", "t", false, "TTY 模式（预留：Phase 1 不分配 PTY）")
	runCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "保持 STDIN 打开（预留：Phase 1 默认已透传 STDIN）")

	// Phase 2 新增：rootfs 参数
	runCmd.Flags().StringVar(&rootfs, "rootfs", "", "容器根文件系统路径（例如：busybox 解压目录）")

	// Phase 3 新增：后台运行
	runCmd.Flags().BoolVarP(&detach, "detach", "d", false, "后台运行容器并输出容器 ID")

	// Phase 6 新增：资源限制
	runCmd.Flags().StringVarP(&memoryLimit, "memory", "m", "", "内存限制（例如: 512m, 1g）")
	runCmd.Flags().StringVar(&memorySwap, "memory-swap", "", "内存+交换空间限制（-1 不限制）")
	runCmd.Flags().StringVar(&cpus, "cpus", "", "CPU 核数限制（例如: 0.5, 2）")
	runCmd.Flags().Int64Var(&cpuQuota, "cpu-quota", 0, "CPU 配额（微秒，高级用户）")
	runCmd.Flags().Int64Var(&cpuPeriod, "cpu-period", 100000, "CPU 周期（微秒，默认 100000）")
	runCmd.Flags().Int64Var(&pidsLimit, "pids-limit", 0, "进程数限制")

	// Phase 7 新增：网络配置
	runCmd.Flags().StringVar(&networkMode, "network", "bridge", "网络模式（bridge/host/none）")
	runCmd.Flags().StringArrayVarP(&publishPorts, "publish", "p", nil, "发布端口（格式: [hostIP:]hostPort:containerPort[/protocol]）")

	// Phase 11 预留：容器名称（当前不实现）
	// runCmd.Flags().StringVar(&name, "name", "", "容器名称")
}

func runContainer(cmd *cobra.Command, args []string) error {
	// Phase 2: rootfs 路径验证（在父进程中验证，避免子进程启动失败）
	if rootfs != "" {
		// 转换为绝对路径（避免 chdir 后路径错乱）
		absRootfs, err := filepath.Abs(rootfs)
		if err != nil {
			return fmt.Errorf("invalid rootfs path: %w", err)
		}

		// 验证 rootfs 存在且可访问
		if info, err := os.Stat(absRootfs); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("rootfs does not exist: %s", absRootfs)
			}
			return fmt.Errorf("cannot access rootfs: %w", err)
		} else if !info.IsDir() {
			return fmt.Errorf("rootfs is not a directory: %s", absRootfs)
		}

		rootfs = absRootfs
	}

	// Phase 6: 解析资源限制
	cgroupConfig, err := parseCgroupFlags()
	if err != nil {
		return fmt.Errorf("invalid resource limits: %w", err)
	}

	// Phase 6: 检查 cgroup v2 支持
	if cgroupConfig != nil && !cgroupConfig.IsEmpty() {
		if !cgroups.IsCgroupV2() {
			return fmt.Errorf("resource limits require cgroup v2, but system uses cgroup v1")
		}
	}

	// Phase 7: 解析网络配置
	networkConfig, err := parseNetworkFlags()
	if err != nil {
		return fmt.Errorf("invalid network configuration: %w", err)
	}

	// Phase 3: 初始化状态存储
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	config := &runtime.ContainerConfig{
		Command: args[0:1],
		Args:    args[1:],
		// Phase 1: 记录 `-t` 但不分配 PTY（见 docs/phase1-dev-notes.md）。
		TTY:           tty,
		Rootfs:        rootfs,        // Phase 2 新增
		Detached:      detach,        // Phase 3 新增
		CgroupConfig:  cgroupConfig,  // Phase 6 新增
		NetworkConfig: networkConfig, // Phase 7 新增
	}

	// 生成容器 ID（64位十六进制，前12位用作默认主机名）
	config.ID = runtime.GenerateContainerID()
	config.Hostname = config.ID[:12]

	// Phase 3: 传入状态存储
	exitCode, err := runtime.Run(config, &runtime.RunOptions{
		StateStore: store,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Phase 3: 后台模式输出容器 ID
	if detach {
		fmt.Println(config.ID)
	}

	os.Exit(exitCode)
	return nil // unreachable
}

// parseCgroupFlags 解析资源限制参数
func parseCgroupFlags() (*cgroups.CgroupConfig, error) {
	config := &cgroups.CgroupConfig{}

	// 解析内存限制 (支持 k/m/g 后缀)
	if memoryLimit != "" {
		bytes, err := parseMemoryString(memoryLimit)
		if err != nil {
			return nil, fmt.Errorf("invalid memory limit: %w", err)
		}
		if bytes < 0 {
			return nil, fmt.Errorf("memory must be non-negative")
		}
		config.Memory = bytes
	}

	// 解析内存+交换空间限制
	if memorySwap != "" {
		// 语义对齐 Docker：
		// - --memory-swap 表示 memory+swap 的总上限
		// - 需要同时设置 --memory（否则语义不清晰，也无法换算 swap.max）
		if config.Memory == 0 {
			return nil, fmt.Errorf("memory-swap requires --memory to be set")
		}

		if memorySwap == "-1" {
			config.MemorySwap = -1
		} else {
			bytes, err := parseMemoryString(memorySwap)
			if err != nil {
				return nil, fmt.Errorf("invalid memory-swap limit: %w", err)
			}
			if bytes <= 0 {
				return nil, fmt.Errorf("memory-swap must be -1 or a positive value")
			}
			if bytes < config.Memory {
				return nil, fmt.Errorf("memory-swap must be >= memory (got memory=%d, memory-swap=%d)", config.Memory, bytes)
			}
			config.MemorySwap = bytes
		}
	}

	// 解析 CPU 限制 (cpus -> quota)
	// cgroup v2 cpu.max period 有效范围: 1000-1000000 微秒 (1ms - 1s)
	const (
		minCPUPeriod = 1000
		maxCPUPeriod = 1000000
	)
	if cpuPeriod < minCPUPeriod || cpuPeriod > maxCPUPeriod {
		return nil, fmt.Errorf("cpu-period must be between %d and %d (got %d)", minCPUPeriod, maxCPUPeriod, cpuPeriod)
	}

	if cpus != "" {
		if cpuQuota > 0 {
			return nil, fmt.Errorf("cannot set both --cpus and --cpu-quota")
		}

		cpuFloat, err := strconv.ParseFloat(cpus, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid cpus value: %w", err)
		}
		if cpuFloat <= 0 {
			return nil, fmt.Errorf("cpus must be positive")
		}
		// 转换为 quota: cpus * period
		config.CPUPeriod = cpuPeriod
		config.CPUQuota = int64(cpuFloat * float64(cpuPeriod))
		if config.CPUQuota <= 0 {
			return nil, fmt.Errorf("cpus too small (computed quota=%d)", config.CPUQuota)
		}
	} else if cpuQuota > 0 {
		config.CPUQuota = cpuQuota
		config.CPUPeriod = cpuPeriod
	}

	// 进程数限制
	if pidsLimit < 0 {
		return nil, fmt.Errorf("pids-limit must be non-negative")
	}
	if pidsLimit > 0 {
		config.PidsLimit = pidsLimit
	}

	return config, nil
}

// parseMemoryString 解析内存字符串（如 "512m" -> 536870912）
// 支持 b/k/kb/m/mb/g/gb 后缀（不区分大小写），数字部分可为整数或小数（如 1.5g）。
func parseMemoryString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty memory string")
	}

	s = strings.ToLower(s)

	var multiplier int64 = 1
	numStr := s

	// 注意：必须先匹配 "kb/mb/gb"，否则会被末尾的 "b" 分支抢先匹配
	switch {
	case strings.HasSuffix(s, "kb"):
		multiplier = 1024
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "k"):
		multiplier = 1024
		numStr = s[:len(s)-1]
	case strings.HasSuffix(s, "mb"):
		multiplier = 1024 * 1024
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "m"):
		multiplier = 1024 * 1024
		numStr = s[:len(s)-1]
	case strings.HasSuffix(s, "gb"):
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "g"):
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-1]
	case strings.HasSuffix(s, "b"):
		multiplier = 1
		numStr = s[:len(s)-1]
	default:
		// 纯数字，假设为字节
		multiplier = 1
		numStr = s
	}

	numStr = strings.TrimSpace(numStr)
	if numStr == "" {
		return 0, fmt.Errorf("missing number")
	}

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", numStr)
	}
	if num < 0 {
		return 0, fmt.Errorf("memory value must be non-negative")
	}

	// overflow guard
	if num > float64(math.MaxInt64)/float64(multiplier) {
		return 0, fmt.Errorf("memory value too large")
	}

	return int64(num * float64(multiplier)), nil
}

// parseNetworkFlags 解析网络配置参数
func parseNetworkFlags() (*network.NetworkConfig, error) {
	config := &network.NetworkConfig{}

	// 解析网络模式
	switch strings.ToLower(networkMode) {
	case "bridge":
		config.Mode = network.NetworkModeBridge
	case "host":
		config.Mode = network.NetworkModeHost
	case "none":
		config.Mode = network.NetworkModeNone
	default:
		return nil, fmt.Errorf("unsupported network mode: %s (supported: bridge, host, none)", networkMode)
	}

	// 解析端口映射（仅 bridge 模式支持）
	if len(publishPorts) > 0 {
		if config.Mode != network.NetworkModeBridge {
			return nil, fmt.Errorf("port mapping (-p) is only supported in bridge network mode")
		}

		for _, portSpec := range publishPorts {
			pm, err := parsePortMapping(portSpec)
			if err != nil {
				return nil, fmt.Errorf("invalid port mapping %q: %w", portSpec, err)
			}
			config.PortMappings = append(config.PortMappings, pm)
		}
	}

	return config, nil
}

// parsePortMapping 解析端口映射字符串
// 支持格式:
//   - hostPort:containerPort (例如: 8080:80)
//   - hostPort:containerPort/protocol (例如: 8080:80/tcp)
//   - hostIP:hostPort:containerPort (例如: 127.0.0.1:8080:80)
//   - hostIP:hostPort:containerPort/protocol (例如: 127.0.0.1:8080:80/tcp)
func parsePortMapping(spec string) (network.PortMapping, error) {
	pm := network.PortMapping{
		Protocol: "tcp", // 默认 TCP
	}

	// 分离协议部分
	if idx := strings.LastIndex(spec, "/"); idx != -1 {
		protocol := strings.ToLower(spec[idx+1:])
		if protocol != "tcp" && protocol != "udp" {
			return pm, fmt.Errorf("unsupported protocol: %s (supported: tcp, udp)", protocol)
		}
		pm.Protocol = protocol
		spec = spec[:idx]
	}

	// 分离 hostIP:hostPort:containerPort 或 hostPort:containerPort
	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 2:
		// hostPort:containerPort
		hostPort, err := parsePort(parts[0])
		if err != nil {
			return pm, fmt.Errorf("invalid host port: %w", err)
		}
		containerPort, err := parsePort(parts[1])
		if err != nil {
			return pm, fmt.Errorf("invalid container port: %w", err)
		}
		pm.HostPort = hostPort
		pm.ContainerPort = containerPort
	case 3:
		// hostIP:hostPort:containerPort
		pm.HostIP = parts[0]
		hostPort, err := parsePort(parts[1])
		if err != nil {
			return pm, fmt.Errorf("invalid host port: %w", err)
		}
		containerPort, err := parsePort(parts[2])
		if err != nil {
			return pm, fmt.Errorf("invalid container port: %w", err)
		}
		pm.HostPort = hostPort
		pm.ContainerPort = containerPort
	default:
		return pm, fmt.Errorf("invalid format, expected hostPort:containerPort or hostIP:hostPort:containerPort")
	}

	return pm, nil
}

// parsePort 解析端口号字符串
func parsePort(s string) (uint16, error) {
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port number: %s", s)
	}
	if port == 0 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return uint16(port), nil
}
