//go:build linux
// +build linux

package cgroups

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultCgroupRoot 是 cgroup v2 的默认挂载点
	DefaultCgroupRoot = "/sys/fs/cgroup"

	// CgroupMinidockerPrefix 是 minidocker 容器的 cgroup 路径前缀
	CgroupMinidockerPrefix = "minidocker"
)

// IsCgroupV2 检查系统是否运行 cgroup v2（统一层级）。
//
// 检测方法：
// 1. 检查 /sys/fs/cgroup/cgroup.controllers 是否存在
// 2. 这是 cgroup v2 统一层级的标志性文件
func IsCgroupV2() bool {
	_, err := os.Stat(filepath.Join(DefaultCgroupRoot, "cgroup.controllers"))
	return err == nil
}

// DetectCgroupV2Root 检测 cgroup v2 挂载点。
//
// 返回 cgroup v2 的根路径（通常为 /sys/fs/cgroup）。
// 如果系统不支持 cgroup v2，返回错误。
func DetectCgroupV2Root() (string, error) {
	if !IsCgroupV2() {
		return "", fmt.Errorf("system does not support cgroup v2 (unified hierarchy); " +
			"cgroup v2 is required for resource limits; " +
			"see https://wiki.archlinux.org/title/Cgroup for migration guide")
	}

	// 验证挂载点类型
	if err := verifyCgroup2Mount(DefaultCgroupRoot); err != nil {
		return "", err
	}

	return DefaultCgroupRoot, nil
}

// verifyCgroup2Mount 验证路径是否为 cgroup2 挂载点
func verifyCgroup2Mount(path string) error {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return fmt.Errorf("open /proc/mounts: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 {
			mountPoint := fields[1]
			fsType := fields[2]
			if mountPoint == path && fsType == "cgroup2" {
				return nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read /proc/mounts: %w", err)
	}

	return fmt.Errorf("%s is not a cgroup2 mount", path)
}

// GetAvailableControllers 获取当前 cgroup 中可用的控制器列表。
//
// 返回的控制器列表来自 cgroup.controllers 文件。
func GetAvailableControllers(root string) ([]string, error) {
	controllersPath := filepath.Join(root, "cgroup.controllers")
	data, err := os.ReadFile(controllersPath)
	if err != nil {
		return nil, fmt.Errorf("read cgroup.controllers: %w", err)
	}

	controllers := strings.Fields(string(data))
	return controllers, nil
}

// CheckRequiredControllers 检查是否有所需的控制器可用。
//
// 根据 CgroupConfig 中配置的资源限制，检查对应的控制器是否可用。
func CheckRequiredControllers(root string, config *CgroupConfig) error {
	if config == nil || config.IsEmpty() {
		return nil
	}

	controllers, err := GetAvailableControllers(root)
	if err != nil {
		return err
	}

	controllerSet := make(map[string]bool)
	for _, c := range controllers {
		controllerSet[c] = true
	}

	// 检查内存控制器
	if config.Memory > 0 || config.MemorySwap != 0 {
		if !controllerSet["memory"] {
			return fmt.Errorf("memory controller not available; " +
				"ensure 'memory' is in cgroup.controllers")
		}
	}

	// 检查 CPU 控制器
	if config.CPUQuota > 0 {
		if !controllerSet["cpu"] {
			return fmt.Errorf("cpu controller not available; " +
				"ensure 'cpu' is in cgroup.controllers")
		}
	}

	// 检查 pids 控制器
	if config.PidsLimit > 0 {
		if !controllerSet["pids"] {
			return fmt.Errorf("pids controller not available; " +
				"ensure 'pids' is in cgroup.controllers")
		}
	}

	return nil
}

// GetCgroupPath 返回容器的完整 cgroup 路径。
//
// 格式: /sys/fs/cgroup/minidocker/<container-id>
func GetCgroupPath(containerID string) string {
	return filepath.Join(CgroupMinidockerPrefix, containerID)
}
