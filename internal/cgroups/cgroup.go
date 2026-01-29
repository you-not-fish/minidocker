//go:build linux
// +build linux

// Package cgroups 提供 cgroup v2 资源限制管理功能。
//
// Phase 6 实现：
// - 内存限制 (memory.max, memory.swap.max)
// - CPU 限制 (cpu.max)
// - 进程数限制 (pids.max)
//
// 设计决策：
// - 仅支持 cgroup v2（统一层级），v1 兼容可作为后续可选阶段
// - cgroup 路径格式：/sys/fs/cgroup/minidocker/<container-id>/
// - 对齐 runc/containerd 的实现模式
package cgroups

import (
	"os"
	"strconv"
	"strings"
)

// CgroupConfig 定义容器的资源限制配置。
// 对齐 cgroup v2 控制器接口。
type CgroupConfig struct {
	// Memory 内存限制（字节）
	// 对应 memory.max
	// 0 表示不限制
	Memory int64 `json:"memory,omitempty"`

	// MemorySwap 内存+交换空间总限制（字节）
	// 语义对齐 Docker 的 --memory-swap（total = memory + swap）。
	//
	// 注意：cgroup v2 中真正写入的是 memory.swap.max（swap 上限），因此实现会换算：
	//   swap.max = MemorySwap - Memory
	//
	// -1 表示不限制 swap（memory.swap.max = "max"）
	// 0 表示不设置 swap 限制
	// > 0 表示 memory+swap 总上限
	MemorySwap int64 `json:"memorySwap,omitempty"`

	// CPUQuota CPU 配额（微秒/周期）
	// 对应 cpu.max 的 quota 部分
	// 例如: 50000 表示 50ms/100ms = 50% 单核
	// 0 表示不限制
	CPUQuota int64 `json:"cpuQuota,omitempty"`

	// CPUPeriod CPU 周期（微秒）
	// 对应 cpu.max 的 period 部分
	// 默认 100000 (100ms)
	CPUPeriod int64 `json:"cpuPeriod,omitempty"`

	// PidsLimit 进程数限制
	// 对应 pids.max
	// 0 表示不限制
	PidsLimit int64 `json:"pidsLimit,omitempty"`
}

// IsEmpty 检查是否有任何资源限制配置。
// 如果所有限制都为零值，则认为没有配置资源限制。
func (c *CgroupConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.Memory == 0 && c.MemorySwap == 0 &&
		c.CPUQuota == 0 && c.PidsLimit == 0
}

// Manager 定义 cgroup 管理器接口。
// 当前仅实现 v2，预留 v1 扩展点。
type Manager interface {
	// Create 创建 cgroup 目录并应用资源限制。
	// cgroupPath 是相对于 cgroup 根的路径，如 "minidocker/<container-id>"
	Create(cgroupPath string, config *CgroupConfig) error

	// Apply 将进程加入 cgroup。
	// pid 是要加入的进程 ID。
	Apply(cgroupPath string, pid int) error

	// Update 更新 cgroup 资源限制（预留：Phase 11 运行时调整）。
	Update(cgroupPath string, config *CgroupConfig) error

	// Destroy 删除 cgroup（容器退出时清理）。
	Destroy(cgroupPath string) error

	// GetStats 获取 cgroup 统计信息（用于 inspect/监控）。
	GetStats(cgroupPath string) (*Stats, error)

	// GetPath 返回 cgroup 的完整路径。
	GetPath(cgroupPath string) string
}

// Stats 保存 cgroup 统计信息（用于 inspect/监控）。
type Stats struct {
	// Memory
	MemoryUsage   int64 `json:"memoryUsage"`
	MemoryLimit   int64 `json:"memoryLimit"`
	MemoryMaxUsed int64 `json:"memoryMaxUsed,omitempty"`

	// CPU
	CPUUsage int64 `json:"cpuUsage"` // 纳秒

	// Pids
	PidsCount int64 `json:"pidsCount"`
	PidsLimit int64 `json:"pidsLimit"`

	// OOM
	OOMKillCount int64 `json:"oomKillCount,omitempty"`
}

// NewManager 创建一个新的 cgroup 管理器。
// 自动检测 cgroup 版本并返回对应的管理器。
// 当前仅支持 cgroup v2。
func NewManager() (Manager, error) {
	return NewV2Manager()
}

// writeFile 是写入 cgroup 控制文件的辅助函数。
func writeFile(path, value string) error {
	return os.WriteFile(path, []byte(value), 0644)
}

// readFile 是读取 cgroup 控制文件的辅助函数。
func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readInt64 读取 cgroup 控制文件并解析为 int64。
func readInt64(path string) (int64, error) {
	data, err := readFile(path)
	if err != nil {
		return 0, err
	}
	data = strings.TrimSpace(data)
	return strconv.ParseInt(data, 10, 64)
}
