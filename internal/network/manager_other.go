//go:build !linux
// +build !linux

package network

import "fmt"

// NewManager 创建新的网络管理器（非 Linux 平台 stub）
func NewManager(dataDir string) (Manager, error) {
	return nil, fmt.Errorf("network management is only supported on Linux")
}
