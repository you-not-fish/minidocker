//go:build linux
// +build linux

package network

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"minidocker/pkg/fileutil"
)

// IPAM 定义 IP 地址管理接口
type IPAM interface {
	// Allocate 为容器分配 IP 地址
	Allocate(containerID string) (string, error)

	// Release 释放容器的 IP 地址
	Release(containerID string) error

	// Get 获取容器的 IP 地址（如果已分配）
	Get(containerID string) (string, bool)
}

// IPAMConfig 保存 IPAM 的持久化状态
type IPAMConfig struct {
	// Subnet 是管理的子网（如 172.17.0.0/16）
	Subnet string `json:"subnet"`

	// Gateway 是网关 IP（如 172.17.0.1）
	Gateway string `json:"gateway"`

	// Allocated 保存已分配的 IP 映射（containerID -> IP）
	Allocated map[string]string `json:"allocated"`

	// LastAllocated 是最后分配的 IP 的主机部分（用于顺序分配）
	LastAllocated uint32 `json:"lastAllocated"`
}

// ipamManager 实现 IPAM 接口
type ipamManager struct {
	mu       sync.Mutex
	dataDir  string
	subnet   *net.IPNet
	gateway  net.IP
	filePath string
}

// NewIPAM 创建新的 IPAM 管理器
func NewIPAM(dataDir string) (IPAM, error) {
	// 解析默认子网
	_, subnet, err := net.ParseCIDR(DefaultSubnet)
	if err != nil {
		return nil, fmt.Errorf("parse default subnet: %w", err)
	}

	// 解析默认网关
	gateway := net.ParseIP(DefaultGateway)
	if gateway == nil {
		return nil, fmt.Errorf("parse default gateway: %s", DefaultGateway)
	}

	// 确保数据目录存在
	networkDir := filepath.Join(dataDir, "network")
	if err := os.MkdirAll(networkDir, 0755); err != nil {
		return nil, fmt.Errorf("create network directory: %w", err)
	}

	return &ipamManager{
		dataDir:  dataDir,
		subnet:   subnet,
		gateway:  gateway,
		filePath: filepath.Join(networkDir, "ipam.json"),
	}, nil
}

// Allocate 为容器分配 IP 地址
func (m *ipamManager) Allocate(containerID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 加载当前状态
	config, err := m.load()
	if err != nil {
		return "", err
	}

	// 检查是否已分配
	if ip, ok := config.Allocated[containerID]; ok {
		return ip, nil
	}

	// 计算子网范围
	// 对于 172.17.0.0/16，范围是 172.17.0.1 到 172.17.255.254
	ones, bits := m.subnet.Mask.Size()
	hostBits := bits - ones
	maxHosts := uint32(1<<hostBits) - 2 // 减去网络地址和广播地址

	// 从上次分配的位置开始查找
	startHost := config.LastAllocated + 1
	if startHost < 2 {
		startHost = 2 // 跳过 .0（网络地址）和 .1（网关）
	}

	// 获取子网的网络地址（如 172.17.0.0）
	networkIP := binary.BigEndian.Uint32(m.subnet.IP.To4())

	// 查找可用的 IP
	for i := uint32(0); i < maxHosts; i++ {
		hostPart := (startHost + i - 2) % (maxHosts - 1) + 2 // 在 2 到 maxHosts 之间循环
		candidateIP := networkIP + hostPart

		// 转换为 IP 字符串
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, candidateIP)
		ip := net.IP(ipBytes).String()

		// 检查是否已被使用
		used := false
		for _, allocatedIP := range config.Allocated {
			if allocatedIP == ip {
				used = true
				break
			}
		}

		if !used {
			// 找到可用 IP，分配它
			config.Allocated[containerID] = ip
			config.LastAllocated = hostPart

			if err := m.save(config); err != nil {
				return "", fmt.Errorf("save IPAM state: %w", err)
			}

			return ip, nil
		}
	}

	return "", fmt.Errorf("no available IP addresses in subnet %s", m.subnet.String())
}

// Release 释放容器的 IP 地址
func (m *ipamManager) Release(containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	config, err := m.load()
	if err != nil {
		return err
	}

	// 检查是否已分配
	if _, ok := config.Allocated[containerID]; !ok {
		// 未分配，静默返回（幂等）
		return nil
	}

	// 删除分配
	delete(config.Allocated, containerID)

	return m.save(config)
}

// Get 获取容器的 IP 地址
func (m *ipamManager) Get(containerID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	config, err := m.load()
	if err != nil {
		return "", false
	}

	ip, ok := config.Allocated[containerID]
	return ip, ok
}

// load 加载 IPAM 配置
func (m *ipamManager) load() (*IPAMConfig, error) {
	config := &IPAMConfig{
		Subnet:    DefaultSubnet,
		Gateway:   DefaultGateway,
		Allocated: make(map[string]string),
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return nil, fmt.Errorf("read IPAM file: %w", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("parse IPAM file: %w", err)
	}

	// 确保 Allocated map 已初始化
	if config.Allocated == nil {
		config.Allocated = make(map[string]string)
	}

	return config, nil
}

// save 保存 IPAM 配置
func (m *ipamManager) save(config *IPAMConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IPAM config: %w", err)
	}

	return fileutil.AtomicWriteFile(m.filePath, data, 0644)
}
