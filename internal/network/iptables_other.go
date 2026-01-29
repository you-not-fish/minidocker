//go:build !linux
// +build !linux

package network

import "fmt"

// iptablesManager 管理 iptables 规则（非 Linux 平台 stub）
type iptablesManager struct{}

func newIptablesManager(bridgeName string) (*iptablesManager, error) {
	return nil, fmt.Errorf("iptables is only supported on Linux")
}

func (m *iptablesManager) SetupMasquerade() error {
	return fmt.Errorf("iptables is only supported on Linux")
}

func (m *iptablesManager) TeardownMasquerade() error {
	return fmt.Errorf("iptables is only supported on Linux")
}

func (m *iptablesManager) SetupPortMapping(containerIP string, mapping PortMapping) error {
	return fmt.Errorf("iptables is only supported on Linux")
}

func (m *iptablesManager) TeardownPortMapping(containerIP string, mapping PortMapping) error {
	return fmt.Errorf("iptables is only supported on Linux")
}

func (m *iptablesManager) SetupForwardAccept() error {
	return fmt.Errorf("iptables is only supported on Linux")
}
