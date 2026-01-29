//go:build !linux
// +build !linux

package cgroups

import "fmt"

const (
	DefaultCgroupRoot      = "/sys/fs/cgroup"
	CgroupMinidockerPrefix = "minidocker"
)

// IsCgroupV2 检查系统是否运行 cgroup v2。
// 在非 Linux 平台上始终返回 false。
func IsCgroupV2() bool {
	return false
}

// DetectCgroupV2Root 检测 cgroup v2 挂载点。
// 在非 Linux 平台上返回错误。
func DetectCgroupV2Root() (string, error) {
	return "", fmt.Errorf("cgroups are only supported on Linux")
}

// GetAvailableControllers 获取可用的控制器列表。
// 在非 Linux 平台上返回错误。
func GetAvailableControllers(root string) ([]string, error) {
	return nil, fmt.Errorf("cgroups are only supported on Linux")
}

// CheckRequiredControllers 检查是否有所需的控制器可用。
// 在非 Linux 平台上返回错误。
func CheckRequiredControllers(root string, config *CgroupConfig) error {
	return fmt.Errorf("cgroups are only supported on Linux")
}

// GetCgroupPath 返回容器的完整 cgroup 路径。
func GetCgroupPath(containerID string) string {
	return CgroupMinidockerPrefix + "/" + containerID
}
