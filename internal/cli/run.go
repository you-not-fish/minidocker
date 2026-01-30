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
	"minidocker/internal/image"
	"minidocker/internal/network"
	"minidocker/internal/runtime"
	"minidocker/internal/snapshot"
	"minidocker/internal/state"
	"minidocker/internal/volume"

	"github.com/spf13/cobra"
)

var (
	// Run 命令标志
	tty         bool
	interactive bool
	rootfs      string // Phase 2 新增
	detach      bool   // Phase 3 新增：后台运行

	// Phase 11 新增：容器配置
	containerName string   // --name
	hostname      string   // --hostname
	envVars       []string // -e, --env
	workDir       string   // -w, --workdir
	user          string   // -u, --user

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

	// Phase 10 新增：卷挂载
	volumes []string // -v, --volume，如 "/host:/container", "volume:/container:ro"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] [IMAGE] COMMAND [ARG...]",
	Short: "在新容器中运行命令",
	Long: `使用指定命令创建并运行一个新容器。

容器将使用 Linux namespaces 进行隔离：
  - UTS namespace (主机名隔离)
  - PID namespace (进程隔离)
  - Mount namespace (文件系统隔离)
  - IPC namespace (进程间通信隔离)
  - Network namespace (网络隔离，Phase 7)

镜像支持（Phase 9）：
  - 指定镜像名称或 digest 作为第一个参数
  - 使用 --rootfs 显式指定 rootfs 目录（向后兼容）
  - 镜像和 --rootfs 互斥

资源限制（Phase 6，需要 cgroup v2）：
  - 内存限制: -m, --memory
  - CPU 限制: --cpus
  - 进程数限制: --pids-limit

网络模式（Phase 7）：
  - bridge: 默认模式，创建独立网络命名空间并连接到 minidocker0 bridge
  - host: 共享宿主机网络
  - none: 只有 loopback 的独立网络命名空间

卷挂载（Phase 10）：
  - -v /host/path:/container/path      # Bind mount
  - -v /host/path:/container/path:ro   # Bind mount（只读）
  - -v volume_name:/container/path     # Named volume
  - -v volume_name:/container/path:ro  # Named volume（只读）

容器配置（Phase 11）：
  - --name       容器名称，用于引用容器
  - --hostname   容器主机名（默认: 容器 ID 前 12 位）
  - -e, --env    设置环境变量（格式: KEY=VALUE）
  - -w, --workdir 容器内工作目录
  - -u, --user   运行用户（格式: user[:group] 或 uid[:gid]）

示例:
  minidocker run alpine:latest /bin/sh
  minidocker run -it alpine /bin/sh
  minidocker run alpine /bin/echo "Hello from container"
  minidocker run -d alpine /bin/sleep 100
  minidocker run -m 512m --cpus 0.5 alpine /bin/sh
  minidocker run --pids-limit 100 alpine /bin/sh
  minidocker run --network bridge alpine /bin/sh
  minidocker run --network host alpine /bin/sh
  minidocker run -p 8080:80 alpine /bin/httpd
  minidocker run -v /host/data:/data alpine /bin/sh
  minidocker run -v myvolume:/data alpine /bin/sh
  minidocker run --name my-container alpine /bin/sh
  minidocker run --hostname myhost alpine /bin/sh
  minidocker run -e FOO=bar -e BAZ=qux alpine /bin/sh
  minidocker run -w /app alpine /bin/sh
  minidocker run -u nobody alpine /bin/sh
  minidocker run --rootfs /tmp/rootfs /bin/sh`,
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

	// Phase 10 新增：卷挂载
	runCmd.Flags().StringArrayVarP(&volumes, "volume", "v", nil, "绑定挂载或命名卷（格式: /host:/container[:ro] 或 name:/container[:ro]）")

	// Phase 11 新增：容器配置
	runCmd.Flags().StringVar(&containerName, "name", "", "容器名称")
	runCmd.Flags().StringVar(&hostname, "hostname", "", "容器主机名（默认: 容器 ID 前 12 位）")
	runCmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "设置环境变量（格式: KEY=VALUE）")
	runCmd.Flags().StringVarP(&workDir, "workdir", "w", "", "容器内工作目录")
	runCmd.Flags().StringVarP(&user, "user", "u", "", "运行用户（格式: user[:group] 或 uid[:gid]）")
}

func runContainer(cmd *cobra.Command, args []string) error {
	// Phase 9: 解析参数，确定是使用镜像还是 rootfs
	var imageRef string
	var command []string

	if rootfs != "" {
		// 使用 --rootfs：所有参数都是命令
		command = args
	} else {
		// 没有 --rootfs：第一个参数是镜像，其余是命令
		if len(args) < 2 {
			return fmt.Errorf("usage: run [IMAGE] COMMAND [ARG...] or run --rootfs PATH COMMAND [ARG...]")
		}
		imageRef = args[0]
		command = args[1:]
	}

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

	// Phase 10: 解析卷挂载配置
	mounts, err := parseVolumeFlags()
	if err != nil {
		return fmt.Errorf("invalid volume configuration: %w", err)
	}

	// Phase 11: 解析容器配置
	parsedEnvVars, err := parseEnvVars(envVars)
	if err != nil {
		return fmt.Errorf("invalid environment variable: %w", err)
	}

	// Phase 11: 验证容器名称
	if containerName != "" {
		if err := validateContainerName(containerName); err != nil {
			return fmt.Errorf("invalid container name: %w", err)
		}
	}

	// Phase 3: 初始化状态存储
	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	config := &runtime.ContainerConfig{
		Command: command[0:1],
		Args:    command[1:],
		// Phase 1: 记录 `-t` 但不分配 PTY（见 docs/phase1-dev-notes.md）。
		TTY:           tty,
		Rootfs:        rootfs,        // Phase 2 新增
		Detached:      detach,        // Phase 3 新增
		CgroupConfig:  cgroupConfig,  // Phase 6 新增
		NetworkConfig: networkConfig, // Phase 7 新增
		Image:         imageRef,      // Phase 9 新增
		Mounts:        mounts,        // Phase 10 新增
		Name:          containerName, // Phase 11 新增
		Env:           parsedEnvVars, // Phase 11 新增
		WorkingDir:    workDir,       // Phase 11 新增
		User:          user,          // Phase 11 新增
	}

	// 生成容器 ID（64位十六进制，前12位用作默认主机名）
	config.ID = runtime.GenerateContainerID()
	// Phase 11: 支持自定义主机名，默认使用容器 ID 前 12 位
	if hostname != "" {
		config.Hostname = hostname
	} else {
		config.Hostname = config.ID[:12]
	}

	// Phase 9: 如果指定了镜像，使用 snapshotter 准备 rootfs
	if imageRef != "" {
		// 初始化镜像存储
		imageRoot := filepath.Join(store.RootDir, image.DefaultImagesDir)
		imageStore, err := image.NewStore(imageRoot)
		if err != nil {
			return fmt.Errorf("initialize image store: %w", err)
		}

		// 获取镜像
		img, err := imageStore.Get(imageRef)
		if err != nil {
			return fmt.Errorf("image not found: %w", err)
		}

		// 后台模式（-d）：snapshot 由 shim 进程准备与清理（对齐 containerd-shim 模型）。
		// 前台模式：在父进程中准备 snapshot（提取层并挂载 overlay）。
		if !detach {
			// 初始化 snapshotter
			snapshotter, err := snapshot.NewSnapshotter(store.RootDir, imageStore)
			if err != nil {
				return fmt.Errorf("initialize snapshotter: %w", err)
			}

			// 准备 snapshot（提取层并挂载 overlay）
			rootfsPath, err := snapshotter.Prepare(config.ID, img.Manifest, img.Config)
			if err != nil {
				return fmt.Errorf("prepare snapshot: %w", err)
			}

			// 设置 rootfs 路径
			config.Rootfs = rootfsPath
		}
	}

	// Phase 3: 传入状态存储
	exitCode, err := runtime.Run(config, &runtime.RunOptions{
		StateStore: store,
	})
	if err != nil {
		// Phase 9: 如果失败，清理 snapshot
		if imageRef != "" {
			imageRoot := filepath.Join(store.RootDir, image.DefaultImagesDir)
			if imageStore, storeErr := image.NewStore(imageRoot); storeErr == nil {
				if snapshotter, snapErr := snapshot.NewSnapshotter(store.RootDir, imageStore); snapErr == nil {
					_ = snapshotter.Remove(config.ID)
				}
			}
		}
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

// parseVolumeFlags 解析 -v 参数并返回 Mount 配置列表
func parseVolumeFlags() ([]volume.Mount, error) {
	var mounts []volume.Mount

	for _, spec := range volumes {
		mount, err := parseVolumeSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid volume spec %q: %w", spec, err)
		}
		mounts = append(mounts, mount)
	}

	return mounts, nil
}

// parseVolumeSpec 解析单个卷挂载规格
// 支持格式:
//   - /host/path:/container/path[:options]  -> bind mount
//   - volume_name:/container/path[:options] -> named volume
//
// options: ro,rw (逗号分隔)
func parseVolumeSpec(spec string) (volume.Mount, error) {
	var mount volume.Mount

	// 分割规格字符串
	parts := strings.Split(spec, ":")

	var source, target string
	var optionsStr string

	switch len(parts) {
	case 2:
		source, target = parts[0], parts[1]
	case 3:
		source, target, optionsStr = parts[0], parts[1], parts[2]
	default:
		return mount, fmt.Errorf("invalid format, expected source:target[:options]")
	}

	// 验证 source 不为空
	if source == "" {
		return mount, fmt.Errorf("source cannot be empty")
	}

	// 验证 target 是绝对路径
	if !filepath.IsAbs(target) {
		return mount, fmt.Errorf("container path must be absolute: %s", target)
	}

	// 判断挂载类型：绝对路径 = bind mount，否则 = named volume
	if filepath.IsAbs(source) {
		mount.Type = volume.MountTypeBind
		// 验证 source 路径存在（bind mount 要求源路径存在）
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				return mount, fmt.Errorf("source path does not exist: %s", source)
			}
			return mount, fmt.Errorf("cannot access source path: %w", err)
		}
	} else {
		mount.Type = volume.MountTypeVolume
		// 验证卷名有效性
		if !volume.IsValidVolumeName(source) {
			return mount, fmt.Errorf("invalid volume name: %s (must be alphanumeric, can contain hyphen and underscore)", source)
		}
	}

	mount.Source = source
	mount.Target = target

	// 解析选项
	if optionsStr != "" {
		options := strings.Split(optionsStr, ",")
		for _, opt := range options {
			switch strings.ToLower(strings.TrimSpace(opt)) {
			case "ro", "readonly":
				mount.ReadOnly = true
			case "rw":
				mount.ReadOnly = false
			default:
				return mount, fmt.Errorf("unknown option: %s (supported: ro, rw)", opt)
			}
		}
	}

	return mount, nil
}

// parseEnvVars 解析环境变量参数
// 支持格式:
//   - KEY=VALUE: 设置环境变量
//   - KEY: 从宿主环境继承变量值
func parseEnvVars(envs []string) ([]string, error) {
	var result []string

	for _, env := range envs {
		// 检查是否包含 = 号
		if idx := strings.Index(env, "="); idx != -1 {
			// KEY=VALUE 格式
			key := env[:idx]
			if key == "" {
				return nil, fmt.Errorf("empty variable name in %q", env)
			}
			// 验证变量名合法性
			if !isValidEnvName(key) {
				return nil, fmt.Errorf("invalid variable name %q", key)
			}
			result = append(result, env)
		} else {
			// KEY 格式：从宿主环境继承
			if !isValidEnvName(env) {
				return nil, fmt.Errorf("invalid variable name %q", env)
			}
			if value, ok := os.LookupEnv(env); ok {
				result = append(result, env+"="+value)
			}
			// 如果宿主环境没有该变量，静默忽略（对齐 Docker 行为）
		}
	}

	return result, nil
}

// isValidEnvName 检查环境变量名是否合法
// 规则：以字母或下划线开头，后续可以是字母、数字或下划线
func isValidEnvName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && r != '_' {
				return false
			}
		} else {
			if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				return false
			}
		}
	}
	return true
}

// validateContainerName 验证容器名称
// 规则：以字母或数字开头，后续可以是字母、数字、下划线、点或连字符
func validateContainerName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("name too long (max 128 characters)")
	}

	for i, r := range name {
		if i == 0 {
			// 首字符必须是字母或数字
			if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				return fmt.Errorf("name must start with alphanumeric character")
			}
		} else {
			// 后续字符可以是字母、数字、下划线、点或连字符
			if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') &&
				r != '_' && r != '.' && r != '-' {
				return fmt.Errorf("name can only contain alphanumeric characters, underscores, dots, and hyphens")
			}
		}
	}

	return nil
}
