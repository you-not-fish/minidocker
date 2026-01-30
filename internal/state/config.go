//go:build linux
// +build linux

package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"minidocker/pkg/fileutil"
	"minidocker/pkg/idutil"
)

// ContainerConfig 是持久化的容器配置。
// 此结构体序列化为 config.json 保存在容器目录中。
// 一旦创建，配置不可变。
type ContainerConfig struct {
	// 容器 ID（64 字符十六进制）
	ID string `json:"id"`

	// 主命令
	Command []string `json:"command"`

	// 命令参数
	Args []string `json:"args"`

	// 主机名
	Hostname string `json:"hostname"`

	// 根文件系统路径（Phase 2）
	Rootfs string `json:"rootfs,omitempty"`

	// 是否分配 TTY
	TTY bool `json:"tty"`

	// 是否后台运行（Phase 3）
	Detached bool `json:"detached"`

	// --- Phase 6: cgroup 资源限制 ---
	// 内存限制（字节）
	Memory int64 `json:"memory,omitempty"`

	// 内存+交换空间总限制（字节）
	MemorySwap int64 `json:"memorySwap,omitempty"`

	// CPU 配额（微秒/周期）
	CPUQuota int64 `json:"cpuQuota,omitempty"`

	// CPU 周期（微秒）
	CPUPeriod int64 `json:"cpuPeriod,omitempty"`

	// 进程数限制
	PidsLimit int64 `json:"pidsLimit,omitempty"`

	// --- Phase 7: 网络配置 ---
	// 网络模式（bridge/host/none）
	NetworkMode string `json:"networkMode,omitempty"`

	// 端口映射
	PortMappings []PortMapping `json:"portMappings,omitempty"`

	// --- Phase 9: 镜像配置 ---
	// 镜像引用（例如 "alpine:latest" 或 "sha256:..."）
	// 与 Rootfs 互斥：有 Image 时使用 snapshotter 准备 rootfs
	Image string `json:"image,omitempty"`

	// --- Phase 11 预留字段（当前不实现）---
	// Name 容器名称
	// 在 Phase 11 实现完整的名称到 ID 的映射功能
	// Name string `json:"name,omitempty"`

	// --- Phase 11 预留字段 ---
	// Env 环境变量
	// Env []string `json:"env,omitempty"`

	// WorkingDir 工作目录
	// WorkingDir string `json:"workingDir,omitempty"`

	// User 运行用户
	// User string `json:"user,omitempty"`

	// --- Phase 10 预留字段 ---
	// Mounts 挂载点
	// Mounts []Mount `json:"mounts,omitempty"`
}

// Save 保存配置到 config.json
func (c *ContainerConfig) Save(containerDir string) error {
	configPath := filepath.Join(containerDir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// 原子写入：先写临时文件，再重命名
	if err := fileutil.AtomicWriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

// LoadConfig 从容器目录加载配置
func LoadConfig(containerDir string) (*ContainerConfig, error) {
	configPath := filepath.Join(containerDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var config ContainerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	return &config, nil
}

// GetCommand 返回完整命令（命令 + 参数）
func (c *ContainerConfig) GetCommand() []string {
	cmd := make([]string, 0, len(c.Command)+len(c.Args))
	cmd = append(cmd, c.Command...)
	cmd = append(cmd, c.Args...)
	return cmd
}

// ShortID 返回容器 ID 的前 12 个字符
func (c *ContainerConfig) ShortID() string {
	return idutil.ShortID(c.ID)
}

// HasCgroupConfig 检查是否配置了 cgroup 资源限制
func (c *ContainerConfig) HasCgroupConfig() bool {
	return c.Memory > 0 || c.MemorySwap != 0 ||
		c.CPUQuota > 0 || c.PidsLimit > 0
}

// PortMapping 表示端口映射配置
type PortMapping struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"` // tcp/udp
}
