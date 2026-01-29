//go:build !linux
// +build !linux

package cgroups

import "fmt"

// V2Manager 实现 cgroup v2 管理（非 Linux stub）。
type V2Manager struct {
	root string
}

// NewV2Manager 创建 cgroup v2 管理器。
// 在非 Linux 平台上返回错误。
func NewV2Manager() (*V2Manager, error) {
	return nil, fmt.Errorf("cgroup v2 is only supported on Linux")
}

// Create 创建 cgroup。
func (m *V2Manager) Create(cgroupPath string, config *CgroupConfig) error {
	return fmt.Errorf("cgroups are only supported on Linux")
}

// Apply 将进程加入 cgroup。
func (m *V2Manager) Apply(cgroupPath string, pid int) error {
	return fmt.Errorf("cgroups are only supported on Linux")
}

// Update 更新 cgroup 资源限制。
func (m *V2Manager) Update(cgroupPath string, config *CgroupConfig) error {
	return fmt.Errorf("cgroups are only supported on Linux")
}

// Destroy 删除 cgroup。
func (m *V2Manager) Destroy(cgroupPath string) error {
	return fmt.Errorf("cgroups are only supported on Linux")
}

// GetStats 获取 cgroup 统计信息。
func (m *V2Manager) GetStats(cgroupPath string) (*Stats, error) {
	return nil, fmt.Errorf("cgroups are only supported on Linux")
}

// GetPath 返回 cgroup 的完整路径。
func (m *V2Manager) GetPath(cgroupPath string) string {
	return ""
}
