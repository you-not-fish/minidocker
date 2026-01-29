//go:build linux
// +build linux

package network

import (
	"fmt"
	"strconv"
	"strings"
)

// 默认网络配置常量
const (
	// DefaultBridgeName 是默认的 bridge 接口名
	DefaultBridgeName = "minidocker0"

	// DefaultSubnet 是默认的容器子网（对齐 Docker）
	DefaultSubnet = "172.17.0.0/16"

	// DefaultGateway 是默认的网关 IP（bridge IP）
	DefaultGateway = "172.17.0.1"

	// DefaultSubnetMask 是默认的子网掩码位数
	DefaultSubnetMask = 16
)

// NetworkMode 定义容器的网络模式
type NetworkMode string

const (
	// NetworkModeBridge 是 bridge 网络模式（默认）
	// 容器获得独立的网络命名空间，通过 veth pair 连接到 bridge
	NetworkModeBridge NetworkMode = "bridge"

	// NetworkModeHost 是 host 网络模式
	// 容器共享宿主机的网络命名空间
	NetworkModeHost NetworkMode = "host"

	// NetworkModeNone 是 none 网络模式
	// 容器有独立的网络命名空间，但不配置任何网络接口（只有 loopback）
	NetworkModeNone NetworkMode = "none"
)

// PortMapping 定义端口映射配置
type PortMapping struct {
	// HostIP 是宿主机绑定的 IP 地址（可选，默认 0.0.0.0）
	HostIP string `json:"hostIP,omitempty"`

	// HostPort 是宿主机端口
	HostPort uint16 `json:"hostPort"`

	// ContainerPort 是容器端口
	ContainerPort uint16 `json:"containerPort"`

	// Protocol 是协议类型（tcp 或 udp，默认 tcp）
	Protocol string `json:"protocol,omitempty"`
}

// GetProtocol 返回协议类型，默认为 tcp
func (p *PortMapping) GetProtocol() string {
	if p.Protocol == "" {
		return "tcp"
	}
	return strings.ToLower(p.Protocol)
}

// GetHostIP 返回宿主机 IP，默认为 0.0.0.0
func (p *PortMapping) GetHostIP() string {
	if p.HostIP == "" {
		return "0.0.0.0"
	}
	return p.HostIP
}

// String 返回端口映射的字符串表示
func (p *PortMapping) String() string {
	return fmt.Sprintf("%s:%d->%d/%s", p.GetHostIP(), p.HostPort, p.ContainerPort, p.GetProtocol())
}

// NetworkConfig 定义容器的网络配置
type NetworkConfig struct {
	// Mode 是网络模式（bridge/host/none）
	Mode NetworkMode `json:"mode"`

	// BridgeName 是 bridge 接口名（仅 bridge 模式有效）
	BridgeName string `json:"bridgeName,omitempty"`

	// PortMappings 是端口映射列表（仅 bridge 模式有效）
	PortMappings []PortMapping `json:"portMappings,omitempty"`
}

// IsEmpty 返回是否未配置网络
func (c *NetworkConfig) IsEmpty() bool {
	return c == nil || c.Mode == ""
}

// GetMode 返回网络模式，默认为 bridge
func (c *NetworkConfig) GetMode() NetworkMode {
	if c == nil || c.Mode == "" {
		return NetworkModeBridge
	}
	return c.Mode
}

// GetBridgeName 返回 bridge 名称，默认为 minidocker0
func (c *NetworkConfig) GetBridgeName() string {
	if c == nil || c.BridgeName == "" {
		return DefaultBridgeName
	}
	return c.BridgeName
}

// NeedsNetworkNamespace 返回是否需要创建网络命名空间
// bridge 和 none 模式需要独立的网络命名空间
// host 模式共享宿主机网络命名空间
func (c *NetworkConfig) NeedsNetworkNamespace() bool {
	mode := c.GetMode()
	return mode == NetworkModeBridge || mode == NetworkModeNone
}

// NetworkState 保存容器的网络运行时状态
type NetworkState struct {
	// Mode 是网络模式
	Mode NetworkMode `json:"mode"`

	// IPAddress 是容器的 IP 地址（仅 bridge 模式）
	IPAddress string `json:"ipAddress,omitempty"`

	// Gateway 是网关地址
	Gateway string `json:"gateway,omitempty"`

	// MacAddress 是容器网卡的 MAC 地址
	MacAddress string `json:"macAddress,omitempty"`

	// VethHost 是宿主机侧的 veth 接口名
	VethHost string `json:"vethHost,omitempty"`

	// VethContainer 是容器侧的 veth 接口名
	VethContainer string `json:"vethContainer,omitempty"`

	// PortMappings 是实际的端口映射（包含分配后的值）
	PortMappings []PortMapping `json:"portMappings,omitempty"`
}

// Manager 定义网络管理器接口
type Manager interface {
	// Setup 为容器配置网络
	// containerID: 容器 ID
	// config: 网络配置
	// pid: 容器 init 进程的 PID（用于 setns）
	// 返回网络状态或错误
	Setup(containerID string, config *NetworkConfig, pid int) (*NetworkState, error)

	// Teardown 清理容器的网络资源
	// containerID: 容器 ID
	// state: 之前 Setup 返回的网络状态
	Teardown(containerID string, state *NetworkState) error

	// EnsureBridge 确保 bridge 接口存在
	// 如果不存在则创建，并配置 IP 和 NAT 规则
	EnsureBridge(config *NetworkConfig) error
}

// ParsePortMapping 解析端口映射字符串
// 支持格式：
//   - containerPort (如 "80")
//   - hostPort:containerPort (如 "8080:80")
//   - hostPort:containerPort/protocol (如 "8080:80/tcp")
//   - hostIP:hostPort:containerPort (如 "0.0.0.0:8080:80")
//   - hostIP:hostPort:containerPort/protocol (如 "0.0.0.0:8080:80/udp")
func ParsePortMapping(s string) (PortMapping, error) {
	var pm PortMapping
	pm.Protocol = "tcp" // 默认协议

	// 分离协议部分
	parts := strings.Split(s, "/")
	portPart := parts[0]
	if len(parts) > 1 {
		protocol := strings.ToLower(parts[1])
		if protocol != "tcp" && protocol != "udp" {
			return pm, fmt.Errorf("unsupported protocol: %s (must be tcp or udp)", parts[1])
		}
		pm.Protocol = protocol
	}

	// 分离端口部分
	portParts := strings.Split(portPart, ":")
	switch len(portParts) {
	case 1:
		// 只有 containerPort
		port, err := parsePort(portParts[0])
		if err != nil {
			return pm, fmt.Errorf("invalid container port: %w", err)
		}
		pm.ContainerPort = port
		pm.HostPort = port // 默认使用相同端口
	case 2:
		// hostPort:containerPort
		hostPort, err := parsePort(portParts[0])
		if err != nil {
			return pm, fmt.Errorf("invalid host port: %w", err)
		}
		containerPort, err := parsePort(portParts[1])
		if err != nil {
			return pm, fmt.Errorf("invalid container port: %w", err)
		}
		pm.HostPort = hostPort
		pm.ContainerPort = containerPort
	case 3:
		// hostIP:hostPort:containerPort
		pm.HostIP = portParts[0]
		hostPort, err := parsePort(portParts[1])
		if err != nil {
			return pm, fmt.Errorf("invalid host port: %w", err)
		}
		containerPort, err := parsePort(portParts[2])
		if err != nil {
			return pm, fmt.Errorf("invalid container port: %w", err)
		}
		pm.HostPort = hostPort
		pm.ContainerPort = containerPort
	default:
		return pm, fmt.Errorf("invalid port mapping format: %s", s)
	}

	return pm, nil
}

// parsePort 解析端口字符串
func parsePort(s string) (uint16, error) {
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port number: %s", s)
	}
	if port == 0 {
		return 0, fmt.Errorf("port cannot be 0")
	}
	return uint16(port), nil
}
