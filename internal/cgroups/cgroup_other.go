//go:build !linux
// +build !linux

package cgroups

import "fmt"

// CgroupConfig 定义容器的资源限制配置。
type CgroupConfig struct {
	Memory     int64 `json:"memory,omitempty"`
	MemorySwap int64 `json:"memorySwap,omitempty"`
	CPUQuota   int64 `json:"cpuQuota,omitempty"`
	CPUPeriod  int64 `json:"cpuPeriod,omitempty"`
	PidsLimit  int64 `json:"pidsLimit,omitempty"`
}

// IsEmpty 检查是否有任何资源限制配置。
func (c *CgroupConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.Memory == 0 && c.MemorySwap == 0 &&
		c.CPUQuota == 0 && c.PidsLimit == 0
}

// Manager 定义 cgroup 管理器接口。
type Manager interface {
	Create(cgroupPath string, config *CgroupConfig) error
	Apply(cgroupPath string, pid int) error
	Update(cgroupPath string, config *CgroupConfig) error
	Destroy(cgroupPath string) error
	GetStats(cgroupPath string) (*Stats, error)
	GetPath(cgroupPath string) string
}

// Stats 保存 cgroup 统计信息。
type Stats struct {
	MemoryUsage   int64 `json:"memoryUsage"`
	MemoryLimit   int64 `json:"memoryLimit"`
	MemoryMaxUsed int64 `json:"memoryMaxUsed,omitempty"`
	CPUUsage      int64 `json:"cpuUsage"`
	PidsCount     int64 `json:"pidsCount"`
	PidsLimit     int64 `json:"pidsLimit"`
	OOMKillCount  int64 `json:"oomKillCount,omitempty"`
}

// NewManager 创建一个新的 cgroup 管理器。
// 在非 Linux 平台上返回错误。
func NewManager() (Manager, error) {
	return nil, fmt.Errorf("cgroups are only supported on Linux")
}
