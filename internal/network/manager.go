//go:build linux
// +build linux

package network

import (
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// networkManager 实现 Manager 接口
type networkManager struct {
	dataDir  string
	ipam     IPAM
	bridge   *bridgeDriver
	iptables *iptablesManager
}

// NewManager 创建新的网络管理器
func NewManager(dataDir string) (Manager, error) {
	ipam, err := NewIPAM(dataDir)
	if err != nil {
		return nil, fmt.Errorf("create IPAM: %w", err)
	}

	bridge, err := newBridgeDriver(DefaultBridgeName)
	if err != nil {
		return nil, fmt.Errorf("create bridge driver: %w", err)
	}

	iptables, err := newIptablesManager(DefaultBridgeName)
	if err != nil {
		return nil, fmt.Errorf("create iptables manager: %w", err)
	}

	return &networkManager{
		dataDir:  dataDir,
		ipam:     ipam,
		bridge:   bridge,
		iptables: iptables,
	}, nil
}

// EnsureBridge 确保 bridge 接口存在
func (m *networkManager) EnsureBridge(config *NetworkConfig) error {
	// 创建/确保 bridge 存在
	if err := m.bridge.EnsureBridge(); err != nil {
		return fmt.Errorf("ensure bridge: %w", err)
	}

	// 设置 MASQUERADE NAT 规则
	if err := m.iptables.SetupMasquerade(); err != nil {
		return fmt.Errorf("setup masquerade: %w", err)
	}

	// 设置 FORWARD ACCEPT 规则
	if err := m.iptables.SetupForwardAccept(); err != nil {
		return fmt.Errorf("setup forward accept: %w", err)
	}

	return nil
}

// Setup 为容器配置网络
func (m *networkManager) Setup(containerID string, config *NetworkConfig, pid int) (*NetworkState, error) {
	mode := config.GetMode()

	switch mode {
	case NetworkModeBridge:
		return m.setupBridge(containerID, config, pid)
	case NetworkModeHost:
		return m.setupHost(containerID, config, pid)
	case NetworkModeNone:
		return m.setupNone(containerID, config, pid)
	default:
		return nil, fmt.Errorf("unsupported network mode: %s", mode)
	}
}

// setupBridge 配置 bridge 网络
func (m *networkManager) setupBridge(containerID string, config *NetworkConfig, pid int) (*NetworkState, error) {
	// 分配 IP 地址
	containerIP, err := m.ipam.Allocate(containerID)
	if err != nil {
		return nil, fmt.Errorf("allocate IP: %w", err)
	}

	// 创建 veth pair 并配置
	state, err := m.bridge.SetupVeth(containerID, pid, containerIP)
	if err != nil {
		// 回滚 IP 分配
		_ = m.ipam.Release(containerID)
		return nil, fmt.Errorf("setup veth: %w", err)
	}

	// 设置端口映射
	if len(config.PortMappings) > 0 {
		applied := make([]PortMapping, 0, len(config.PortMappings))
		for _, pm := range config.PortMappings {
			if err := m.iptables.SetupPortMapping(containerIP, pm); err != nil {
				// 回滚已创建的资源
				state.PortMappings = applied
				m.teardownBridge(containerID, state)
				return nil, fmt.Errorf("setup port mapping %s: %w", pm.String(), err)
			}
			applied = append(applied, pm)
		}
		state.PortMappings = applied
	}

	return state, nil
}

// setupHost 配置 host 网络模式
func (m *networkManager) setupHost(containerID string, config *NetworkConfig, pid int) (*NetworkState, error) {
	// host 模式不需要额外配置，容器共享宿主机网络
	return &NetworkState{
		Mode: NetworkModeHost,
	}, nil
}

// setupNone 配置 none 网络模式
func (m *networkManager) setupNone(containerID string, config *NetworkConfig, pid int) (*NetworkState, error) {
	// none 模式只需要启动 loopback
	containerNs, err := netns.GetFromPid(pid)
	if err != nil {
		return nil, fmt.Errorf("get container network namespace: %w", err)
	}
	defer containerNs.Close()

	err = withNetns(containerNs, func() error {
		lo, err := netlink.LinkByName("lo")
		if err != nil {
			return fmt.Errorf("get loopback: %w", err)
		}
		return netlink.LinkSetUp(lo)
	})
	if err != nil {
		return nil, fmt.Errorf("setup loopback: %w", err)
	}

	return &NetworkState{
		Mode: NetworkModeNone,
	}, nil
}

// Teardown 清理容器的网络资源
func (m *networkManager) Teardown(containerID string, state *NetworkState) error {
	if state == nil {
		return nil
	}

	switch state.Mode {
	case NetworkModeBridge:
		return m.teardownBridge(containerID, state)
	case NetworkModeHost:
		// host 模式无需清理
		return nil
	case NetworkModeNone:
		// none 模式无需清理
		return nil
	default:
		return nil
	}
}

// teardownBridge 清理 bridge 网络资源
func (m *networkManager) teardownBridge(containerID string, state *NetworkState) error {
	var lastErr error

	// 清理端口映射
	if len(state.PortMappings) > 0 && state.IPAddress != "" {
		for _, pm := range state.PortMappings {
			if err := m.iptables.TeardownPortMapping(state.IPAddress, pm); err != nil {
				lastErr = err
			}
		}
	}

	// 清理 veth pair
	if state.VethHost != "" {
		if err := m.bridge.TeardownVeth(state.VethHost); err != nil {
			lastErr = err
		}
	}

	// 释放 IP 地址
	if err := m.ipam.Release(containerID); err != nil {
		lastErr = err
	}

	return lastErr
}
