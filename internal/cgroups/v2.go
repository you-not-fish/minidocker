//go:build linux
// +build linux

package cgroups

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// V2Manager 实现 cgroup v2 管理。
type V2Manager struct {
	// root 是 cgroup v2 统一挂载点
	// 通常为 /sys/fs/cgroup
	root string
}

// NewV2Manager 创建 cgroup v2 管理器。
func NewV2Manager() (*V2Manager, error) {
	root, err := DetectCgroupV2Root()
	if err != nil {
		return nil, err
	}
	return &V2Manager{root: root}, nil
}

// Create 创建 cgroup 目录并应用资源限制。
func (m *V2Manager) Create(cgroupPath string, config *CgroupConfig) error {
	fullPath := filepath.Join(m.root, cgroupPath)

	// 如果目录已存在（可能是上次运行残留），尝试清理
	if _, err := os.Stat(fullPath); err == nil {
		if err := m.Destroy(cgroupPath); err != nil {
			return fmt.Errorf("cgroup %s already exists and cannot be removed: %w", cgroupPath, err)
		}
	}

	// 确保父目录存在并启用子树控制器
	parentPath := filepath.Dir(fullPath)
	if err := m.ensureParentControllers(parentPath, config); err != nil {
		return fmt.Errorf("enable parent controllers: %w", err)
	}

	// 创建 cgroup 目录
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return fmt.Errorf("create cgroup directory: %w", err)
	}

	// 应用资源限制
	if err := m.applyConfig(fullPath, config); err != nil {
		// 清理已创建的目录
		_ = os.Remove(fullPath)
		return err
	}

	return nil
}

// ensureParentControllers 确保父 cgroup 启用了所需的控制器。
//
// cgroup v2 要求在父 cgroup 的 cgroup.subtree_control 中启用控制器，
// 子 cgroup 才能使用这些控制器。
func (m *V2Manager) ensureParentControllers(parentPath string, config *CgroupConfig) error {
	if config == nil || config.IsEmpty() {
		return nil
	}

	// 确保 minidocker 目录存在
	if err := os.MkdirAll(parentPath, 0755); err != nil {
		return fmt.Errorf("create parent cgroup: %w", err)
	}

	// 需要启用的控制器
	var controllers []string
	if config.Memory > 0 || config.MemorySwap != 0 {
		controllers = append(controllers, "memory")
	}
	if config.CPUQuota > 0 {
		controllers = append(controllers, "cpu")
	}
	if config.PidsLimit > 0 {
		controllers = append(controllers, "pids")
	}

	if len(controllers) == 0 {
		return nil
	}

	// 从根到父目录逐级启用控制器
	// 需要从 cgroup root 开始逐级启用
	rel, err := filepath.Rel(m.root, parentPath)
	if err != nil {
		return fmt.Errorf("get relative path: %w", err)
	}

	parts := strings.Split(rel, string(filepath.Separator))
	currentPath := m.root

	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}

		// 在当前路径启用子树控制器
		subtreeControlPath := filepath.Join(currentPath, "cgroup.subtree_control")

		// 读取当前已启用的控制器
		enabled := make(map[string]bool)
		if data, err := os.ReadFile(subtreeControlPath); err == nil {
			for _, c := range strings.Fields(string(data)) {
				enabled[c] = true
			}
		}

		// 启用缺失的控制器
		for _, c := range controllers {
			if !enabled[c] {
				// 尝试启用控制器（可能因为父级未启用而失败，忽略错误）
				_ = writeFile(subtreeControlPath, "+"+c)
			}
		}

		currentPath = filepath.Join(currentPath, part)
	}

	return nil
}

// applyConfig 应用资源限制配置。
func (m *V2Manager) applyConfig(cgroupPath string, config *CgroupConfig) error {
	if config == nil || config.IsEmpty() {
		return nil
	}

	// 内存限制
	if config.Memory > 0 {
		memoryMaxPath := filepath.Join(cgroupPath, "memory.max")
		if err := writeFile(memoryMaxPath, strconv.FormatInt(config.Memory, 10)); err != nil {
			return fmt.Errorf("set memory.max: %w", err)
		}
	}

	// 内存+交换空间限制
	if config.MemorySwap != 0 {
		memorySwapMaxPath := filepath.Join(cgroupPath, "memory.swap.max")
		var value string
		if config.MemorySwap == -1 {
			value = "max"
		} else if config.MemorySwap == 0 {
			// 0 表示禁用交换空间
			value = "0"
		} else {
			// 具体值：交换空间限制 = MemorySwap - Memory
			swapLimit := config.MemorySwap - config.Memory
			if swapLimit < 0 {
				swapLimit = 0
			}
			value = strconv.FormatInt(swapLimit, 10)
		}
		if err := writeFile(memorySwapMaxPath, value); err != nil {
			// 交换空间限制可能不可用（例如未启用 swap 或不支持）
			// 记录警告但不失败
			// TODO: 添加日志
		}
	}

	// CPU 限制
	if config.CPUQuota > 0 {
		cpuMaxPath := filepath.Join(cgroupPath, "cpu.max")
		period := config.CPUPeriod
		if period == 0 {
			period = 100000 // 默认 100ms
		}
		value := fmt.Sprintf("%d %d", config.CPUQuota, period)
		if err := writeFile(cpuMaxPath, value); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	}

	// 进程数限制
	if config.PidsLimit > 0 {
		pidsMaxPath := filepath.Join(cgroupPath, "pids.max")
		if err := writeFile(pidsMaxPath, strconv.FormatInt(config.PidsLimit, 10)); err != nil {
			return fmt.Errorf("set pids.max: %w", err)
		}
	}

	return nil
}

// Apply 将进程加入 cgroup。
func (m *V2Manager) Apply(cgroupPath string, pid int) error {
	fullPath := filepath.Join(m.root, cgroupPath)
	procsPath := filepath.Join(fullPath, "cgroup.procs")

	if err := writeFile(procsPath, strconv.Itoa(pid)); err != nil {
		return fmt.Errorf("add process %d to cgroup: %w", pid, err)
	}

	return nil
}

// Update 更新 cgroup 资源限制。
// 预留给 Phase 11 运行时调整功能。
func (m *V2Manager) Update(cgroupPath string, config *CgroupConfig) error {
	fullPath := filepath.Join(m.root, cgroupPath)

	// 检查 cgroup 是否存在
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("cgroup %s does not exist", cgroupPath)
	}

	return m.applyConfig(fullPath, config)
}

// Destroy 删除 cgroup。
func (m *V2Manager) Destroy(cgroupPath string) error {
	fullPath := filepath.Join(m.root, cgroupPath)

	// 检查目录是否存在
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return nil // 目录不存在，认为已清理
	}

	// 检查是否还有进程
	procsPath := filepath.Join(fullPath, "cgroup.procs")
	if data, err := os.ReadFile(procsPath); err == nil {
		procs := strings.TrimSpace(string(data))
		if procs != "" {
			// 仍有进程在 cgroup 中，无法删除
			return fmt.Errorf("cgroup %s still has processes: %s", cgroupPath, procs)
		}
	}

	// 删除 cgroup 目录
	// cgroup 目录只有在没有进程且没有子 cgroup 时才能删除
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("remove cgroup: %w", err)
	}

	// 尝试清理 minidocker 父目录（如果为空）
	parentPath := filepath.Dir(fullPath)
	if filepath.Base(parentPath) == CgroupMinidockerPrefix {
		// 尝试删除，忽略错误（可能还有其他容器）
		_ = os.Remove(parentPath)
	}

	return nil
}

// GetStats 获取 cgroup 统计信息。
func (m *V2Manager) GetStats(cgroupPath string) (*Stats, error) {
	fullPath := filepath.Join(m.root, cgroupPath)

	stats := &Stats{}

	// 内存统计
	if data, err := os.ReadFile(filepath.Join(fullPath, "memory.current")); err == nil {
		stats.MemoryUsage, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}
	if data, err := os.ReadFile(filepath.Join(fullPath, "memory.max")); err == nil {
		value := strings.TrimSpace(string(data))
		if value != "max" {
			stats.MemoryLimit, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	if data, err := os.ReadFile(filepath.Join(fullPath, "memory.peak")); err == nil {
		stats.MemoryMaxUsed, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}

	// CPU 统计
	if data, err := os.ReadFile(filepath.Join(fullPath, "cpu.stat")); err == nil {
		// 解析 cpu.stat 格式: usage_usec <value>
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "usage_usec" {
				usec, _ := strconv.ParseInt(fields[1], 10, 64)
				stats.CPUUsage = usec * 1000 // 转换为纳秒
				break
			}
		}
	}

	// Pids 统计
	if data, err := os.ReadFile(filepath.Join(fullPath, "pids.current")); err == nil {
		stats.PidsCount, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}
	if data, err := os.ReadFile(filepath.Join(fullPath, "pids.max")); err == nil {
		value := strings.TrimSpace(string(data))
		if value != "max" {
			stats.PidsLimit, _ = strconv.ParseInt(value, 10, 64)
		}
	}

	// OOM 统计
	if data, err := os.ReadFile(filepath.Join(fullPath, "memory.events")); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "oom_kill" {
				stats.OOMKillCount, _ = strconv.ParseInt(fields[1], 10, 64)
				break
			}
		}
	}

	return stats, nil
}

// GetPath 返回 cgroup 的完整路径。
func (m *V2Manager) GetPath(cgroupPath string) string {
	return filepath.Join(m.root, cgroupPath)
}
