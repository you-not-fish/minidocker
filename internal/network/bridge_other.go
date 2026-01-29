//go:build !linux
// +build !linux

package network

import "fmt"

// bridgeDriver 实现 bridge 网络模式（非 Linux 平台 stub）
type bridgeDriver struct{}

func newBridgeDriver(bridgeName string) (*bridgeDriver, error) {
	return nil, fmt.Errorf("bridge networking is only supported on Linux")
}

func (d *bridgeDriver) EnsureBridge() error {
	return fmt.Errorf("bridge networking is only supported on Linux")
}

func (d *bridgeDriver) SetupVeth(containerID string, pid int, containerIP string) (*NetworkState, error) {
	return nil, fmt.Errorf("bridge networking is only supported on Linux")
}

func (d *bridgeDriver) TeardownVeth(hostVethName string) error {
	return fmt.Errorf("bridge networking is only supported on Linux")
}
