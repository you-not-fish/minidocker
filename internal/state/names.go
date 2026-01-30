//go:build linux
// +build linux

package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minidocker/pkg/fileutil"
	"minidocker/pkg/idutil"
)

// NamesFile 是名称映射文件的名称
const NamesFile = "names.json"

// nameMapping 存储名称到容器 ID 的映射
type nameMapping struct {
	Names map[string]string `json:"names"` // name -> containerID
}

// NameStore 管理容器名称到 ID 的映射
type NameStore struct {
	rootDir string
	mu      sync.RWMutex
}

// NewNameStore 创建名称存储
func NewNameStore(rootDir string) *NameStore {
	return &NameStore{
		rootDir: rootDir,
	}
}

// namesPath 返回 names.json 文件路径
func (s *NameStore) namesPath() string {
	return filepath.Join(s.rootDir, NamesFile)
}

// load 加载名称映射
func (s *NameStore) load() (*nameMapping, error) {
	mapping := &nameMapping{
		Names: make(map[string]string),
	}

	data, err := os.ReadFile(s.namesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return mapping, nil
		}
		return nil, fmt.Errorf("read names file: %w", err)
	}

	if err := json.Unmarshal(data, mapping); err != nil {
		return nil, fmt.Errorf("parse names file: %w", err)
	}

	if mapping.Names == nil {
		mapping.Names = make(map[string]string)
	}

	return mapping, nil
}

// save 保存名称映射
func (s *NameStore) save(mapping *nameMapping) error {
	data, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal names: %w", err)
	}

	if err := fileutil.AtomicWriteFile(s.namesPath(), data, 0644); err != nil {
		return fmt.Errorf("save names file: %w", err)
	}

	return nil
}

// Register 注册名称到容器 ID 的映射
// 如果名称已存在，返回错误
func (s *NameStore) Register(name, containerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapping, err := s.load()
	if err != nil {
		return err
	}

	// 检查名称是否已存在
	if existingID, exists := mapping.Names[name]; exists {
		return fmt.Errorf("container name %q is already in use by container %s", name, idutil.ShortID(existingID))
	}

	mapping.Names[name] = containerID
	return s.save(mapping)
}

// Unregister 移除名称映射
func (s *NameStore) Unregister(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapping, err := s.load()
	if err != nil {
		return err
	}

	delete(mapping.Names, name)
	return s.save(mapping)
}

// UnregisterByID 根据容器 ID 移除所有关联的名称映射
func (s *NameStore) UnregisterByID(containerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapping, err := s.load()
	if err != nil {
		return err
	}

	// 查找并删除所有指向该容器 ID 的名称
	var toDelete []string
	for name, id := range mapping.Names {
		if id == containerID {
			toDelete = append(toDelete, name)
		}
	}

	for _, name := range toDelete {
		delete(mapping.Names, name)
	}

	return s.save(mapping)
}

// Lookup 通过名称查找容器 ID
// 如果找不到，返回空字符串和 nil error
func (s *NameStore) Lookup(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mapping, err := s.load()
	if err != nil {
		return "", err
	}

	if containerID, exists := mapping.Names[name]; exists {
		return containerID, nil
	}

	return "", nil
}

// GetName 通过容器 ID 查找名称
// 如果找不到，返回空字符串
func (s *NameStore) GetName(containerID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mapping, err := s.load()
	if err != nil {
		return ""
	}

	for name, id := range mapping.Names {
		if id == containerID {
			return name
		}
	}

	return ""
}

// Exists 检查名称是否已存在
func (s *NameStore) Exists(name string) bool {
	containerID, _ := s.Lookup(name)
	return containerID != ""
}
