//go:build !linux
// +build !linux

package network

import "fmt"

// IPAM 定义 IP 地址管理接口
type IPAM interface {
	Allocate(containerID string) (string, error)
	Release(containerID string) error
	Get(containerID string) (string, bool)
}

// IPAMConfig 保存 IPAM 的持久化状态
type IPAMConfig struct {
	Subnet        string            `json:"subnet"`
	Gateway       string            `json:"gateway"`
	Allocated     map[string]string `json:"allocated"`
	LastAllocated uint32            `json:"lastAllocated"`
}

// NewIPAM 创建新的 IPAM 管理器（非 Linux 平台 stub）
func NewIPAM(dataDir string) (IPAM, error) {
	return nil, fmt.Errorf("IPAM is only supported on Linux")
}
