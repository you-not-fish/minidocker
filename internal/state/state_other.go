//go:build !linux
// +build !linux

package state

import "fmt"

// Status 表示容器状态
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
)

// Store 是状态存储的 stub
type Store struct {
	RootDir   string
	NameStore *NameStore
}

// ContainerState 是容器状态的 stub
type ContainerState struct{}

// ContainerConfig 是容器配置的 stub
type ContainerConfig struct{}

// PortMapping 表示端口映射配置
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

// NetworkState 表示容器的网络状态
type NetworkState struct {
	Mode          string
	IPAddress     string
	Gateway       string
	MacAddress    string
	VethHost      string
	VethContainer string
	PortMappings  []PortMapping
}

// ContainerLock 是容器锁的 stub
type ContainerLock struct{}

// NewStore 在非 Linux 平台返回错误
func NewStore(rootDir string) (*Store, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// Create 在非 Linux 平台返回错误
func (s *Store) Create(config *ContainerConfig) (*ContainerState, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// Get 在非 Linux 平台返回错误
func (s *Store) Get(idOrPrefix string) (*ContainerState, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// List 在非 Linux 平台返回错误
func (s *Store) List(all bool) ([]*ContainerState, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// Delete 在非 Linux 平台返回错误
func (s *Store) Delete(containerID string) error {
	return fmt.Errorf("state management requires Linux")
}

// ForceDelete 在非 Linux 平台返回错误
func (s *Store) ForceDelete(containerID string) error {
	return fmt.Errorf("state management requires Linux")
}

// LookupID 在非 Linux 平台返回错误
func (s *Store) LookupID(idOrPrefix string) (string, error) {
	return "", fmt.Errorf("state management requires Linux")
}

// ContainerDir 在非 Linux 平台返回空字符串
func (s *Store) ContainerDir(containerID string) string {
	return ""
}

// Exists 在非 Linux 平台返回 false
func (s *Store) Exists(containerID string) bool {
	return false
}

// AcquireLock 在非 Linux 平台返回错误
func AcquireLock(containerDir string) (*ContainerLock, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// TryAcquireLock 在非 Linux 平台返回错误
func TryAcquireLock(containerDir string) (*ContainerLock, error) {
	return nil, fmt.Errorf("state management requires Linux")
}

// Release 在非 Linux 平台是 no-op
func (l *ContainerLock) Release() error {
	return nil
}
