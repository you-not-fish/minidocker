//go:build linux
// +build linux

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minidocker/pkg/idutil"
)

// 默认状态根目录
const DefaultRootDir = "/var/lib/minidocker"

// 环境变量名
const RootDirEnvVar = "MINIDOCKER_ROOT"

// Store 管理容器状态目录
type Store struct {
	RootDir string
}

// NewStore 创建状态存储。
// rootDir 为空时，按优先级使用：
// 1. MINIDOCKER_ROOT 环境变量
// 2. 默认值 /var/lib/minidocker
func NewStore(rootDir string) (*Store, error) {
	if rootDir == "" {
		rootDir = os.Getenv(RootDirEnvVar)
	}
	if rootDir == "" {
		rootDir = DefaultRootDir
	}

	// 确保根目录存在
	containersDir := filepath.Join(rootDir, "containers")
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		return nil, fmt.Errorf("create containers directory: %w", err)
	}

	return &Store{RootDir: rootDir}, nil
}

// ContainerDir 返回容器目录路径
func (s *Store) ContainerDir(containerID string) string {
	return filepath.Join(s.RootDir, "containers", containerID)
}

// Create 创建容器状态目录和初始状态。
// 返回 creating 状态的 ContainerState。
func (s *Store) Create(config *ContainerConfig) (*ContainerState, error) {
	containerDir := s.ContainerDir(config.ID)

	// 检查是否已存在
	if _, err := os.Stat(containerDir); err == nil {
		return nil, fmt.Errorf("container %s already exists", config.ID)
	}

	// 创建目录结构
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return nil, fmt.Errorf("create container directory: %w", err)
	}

	// 创建日志目录
	logDir := filepath.Join(containerDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		os.RemoveAll(containerDir)
		return nil, fmt.Errorf("create logs directory: %w", err)
	}

	// 保存 config.json
	if err := config.Save(containerDir); err != nil {
		os.RemoveAll(containerDir)
		return nil, fmt.Errorf("save config: %w", err)
	}

	// 创建初始状态
	state := NewState(config.ID, containerDir)
	if err := state.Save(); err != nil {
		os.RemoveAll(containerDir)
		return nil, fmt.Errorf("save state: %w", err)
	}

	return state, nil
}

// Get 获取容器状态。
// 支持短 ID（最少 3 字符）查找。
// 自动检测并修正孤儿状态（进程不存在但状态为 running）。
func (s *Store) Get(idOrPrefix string) (*ContainerState, error) {
	// 解析完整 ID
	fullID, err := s.LookupID(idOrPrefix)
	if err != nil {
		return nil, err
	}

	containerDir := s.ContainerDir(fullID)
	state, err := LoadState(containerDir)
	if err != nil {
		return nil, fmt.Errorf("load state for %s: %w", fullID, err)
	}

	// 触发孤儿检测（如果是 running 状态，会自动修正）
	state.IsRunning()

	return state, nil
}

// List 列出所有容器。
// 如果 all 为 false，只返回运行中的容器。
func (s *Store) List(all bool) ([]*ContainerState, error) {
	containersDir := filepath.Join(s.RootDir, "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read containers directory: %w", err)
	}

	var states []*ContainerState
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		containerDir := filepath.Join(containersDir, entry.Name())
		state, err := LoadState(containerDir)
		if err != nil {
			// 跳过损坏的状态文件
			continue
		}

		// 触发孤儿检测
		state.IsRunning()

		// 如果不是 all 模式，只返回运行中的容器
		if !all && state.Status != StatusRunning {
			continue
		}

		states = append(states, state)
	}

	return states, nil
}

// Delete 删除容器目录。
// 幂等操作：如果容器不存在，返回 nil。
// 如果容器正在运行，返回错误。
func (s *Store) Delete(containerID string) error {
	containerDir := s.ContainerDir(containerID)

	// 检查是否存在
	if _, err := os.Stat(containerDir); os.IsNotExist(err) {
		return nil // 幂等：已删除
	}

	// 加载状态检查是否运行中
	state, err := LoadState(containerDir)
	if err == nil && state.IsRunning() {
		return fmt.Errorf("container %s is running, stop it first or use force", idutil.ShortID(containerID))
	}

	// 删除目录
	if err := os.RemoveAll(containerDir); err != nil {
		return fmt.Errorf("remove container directory: %w", err)
	}

	return nil
}

// Exists 检查容器是否存在
func (s *Store) Exists(containerID string) bool {
	containerDir := s.ContainerDir(containerID)
	_, err := os.Stat(containerDir)
	return err == nil
}

// LookupID 将短 ID 解析为完整 ID。
// 要求至少 3 个字符。
// 如果有多个匹配，返回错误。
func (s *Store) LookupID(idOrPrefix string) (string, error) {
	// 短 ID 至少需要 3 个字符
	if err := idutil.ValidatePrefix(idOrPrefix); err != nil {
		return "", err
	}

	// 如果是完整 ID（64 字符），直接返回
	if idutil.IsFullID(idOrPrefix) {
		if s.Exists(idOrPrefix) {
			return idOrPrefix, nil
		}
		return "", fmt.Errorf("container not found: %s", idOrPrefix)
	}

	// 搜索匹配的容器
	containersDir := filepath.Join(s.RootDir, "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("container not found: %s", idOrPrefix)
		}
		return "", fmt.Errorf("read containers directory: %w", err)
	}

	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, idOrPrefix) {
			matches = append(matches, name)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("container not found: %s", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple containers match prefix %s: %v", idOrPrefix, matches[:min(3, len(matches))])
	}
}

// ForceDelete 强制删除容器（即使正在运行）。
// 调用者负责先发送信号终止进程。
func (s *Store) ForceDelete(containerID string) error {
	containerDir := s.ContainerDir(containerID)

	// 检查是否存在
	if _, err := os.Stat(containerDir); os.IsNotExist(err) {
		return nil // 幂等：已删除
	}

	// 直接删除
	if err := os.RemoveAll(containerDir); err != nil {
		return fmt.Errorf("remove container directory: %w", err)
	}

	return nil
}
