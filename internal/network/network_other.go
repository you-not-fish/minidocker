//go:build !linux
// +build !linux

package network

import "fmt"

// 默认网络配置常量
const (
	DefaultBridgeName = "minidocker0"
	DefaultSubnet     = "172.17.0.0/16"
	DefaultGateway    = "172.17.0.1"
	DefaultSubnetMask = 16
)

// NetworkMode 定义容器的网络模式
type NetworkMode string

const (
	NetworkModeBridge NetworkMode = "bridge"
	NetworkModeHost   NetworkMode = "host"
	NetworkModeNone   NetworkMode = "none"
)

// PortMapping 定义端口映射配置
type PortMapping struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

func (p *PortMapping) GetProtocol() string {
	if p.Protocol == "" {
		return "tcp"
	}
	return p.Protocol
}

func (p *PortMapping) GetHostIP() string {
	if p.HostIP == "" {
		return "0.0.0.0"
	}
	return p.HostIP
}

func (p *PortMapping) String() string {
	return fmt.Sprintf("%s:%d->%d/%s", p.GetHostIP(), p.HostPort, p.ContainerPort, p.GetProtocol())
}

// NetworkConfig 定义容器的网络配置
type NetworkConfig struct {
	Mode         NetworkMode   `json:"mode"`
	BridgeName   string        `json:"bridgeName,omitempty"`
	PortMappings []PortMapping `json:"portMappings,omitempty"`
}

func (c *NetworkConfig) IsEmpty() bool {
	return c == nil || c.Mode == ""
}

func (c *NetworkConfig) GetMode() NetworkMode {
	if c == nil || c.Mode == "" {
		return NetworkModeBridge
	}
	return c.Mode
}

func (c *NetworkConfig) GetBridgeName() string {
	if c == nil || c.BridgeName == "" {
		return DefaultBridgeName
	}
	return c.BridgeName
}

func (c *NetworkConfig) NeedsNetworkNamespace() bool {
	mode := c.GetMode()
	return mode == NetworkModeBridge || mode == NetworkModeNone
}

// NetworkState 保存容器的网络运行时状态
type NetworkState struct {
	Mode          NetworkMode   `json:"mode"`
	IPAddress     string        `json:"ipAddress,omitempty"`
	Gateway       string        `json:"gateway,omitempty"`
	MacAddress    string        `json:"macAddress,omitempty"`
	VethHost      string        `json:"vethHost,omitempty"`
	VethContainer string        `json:"vethContainer,omitempty"`
	PortMappings  []PortMapping `json:"portMappings,omitempty"`
}

// Manager 定义网络管理器接口
type Manager interface {
	Setup(containerID string, config *NetworkConfig, pid int) (*NetworkState, error)
	Teardown(containerID string, state *NetworkState) error
	EnsureBridge(config *NetworkConfig) error
}

// ParsePortMapping 解析端口映射字符串（非 Linux 平台 stub）
func ParsePortMapping(s string) (PortMapping, error) {
	return PortMapping{}, fmt.Errorf("network is only supported on Linux")
}
