//go:build !linux
// +build !linux

package state

import (
	"fmt"
	"runtime"
)

// NameStore 管理容器名称到 ID 的映射
type NameStore struct {
	rootDir string
}

// NewNameStore 创建名称存储
func NewNameStore(rootDir string) *NameStore {
	return &NameStore{rootDir: rootDir}
}

func (s *NameStore) Register(name, containerID string) error {
	return fmt.Errorf("name store is only supported on Linux (current OS: %s)", runtime.GOOS)
}

func (s *NameStore) Unregister(name string) error {
	return fmt.Errorf("name store is only supported on Linux (current OS: %s)", runtime.GOOS)
}

func (s *NameStore) UnregisterByID(containerID string) error {
	return fmt.Errorf("name store is only supported on Linux (current OS: %s)", runtime.GOOS)
}

func (s *NameStore) Lookup(name string) (string, error) {
	return "", fmt.Errorf("name store is only supported on Linux (current OS: %s)", runtime.GOOS)
}

func (s *NameStore) GetName(containerID string) string {
	return ""
}

func (s *NameStore) Exists(name string) bool {
	return false
}
