//go:build linux
// +build linux

package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"minidocker/pkg/fileutil"
)

// Status 表示容器状态
type Status string

const (
	// StatusCreating 表示容器正在初始化中
	StatusCreating Status = "creating"
	// StatusRunning 表示容器正在运行
	StatusRunning Status = "running"
	// StatusStopped 表示容器已停止
	StatusStopped Status = "stopped"
)

// OCI 版本
const OCIVersionCurrent = "1.0.2"

// ContainerState 表示容器的运行时状态。
// 该结构体对齐 OCI Runtime Spec 的 state JSON 格式。
type ContainerState struct {
	// OCI 标准字段
	OCIVersion string `json:"ociVersion"`
	ID         string `json:"id"`
	Status     Status `json:"status"`
	Pid        int    `json:"pid,omitempty"`
	Bundle     string `json:"bundle"`

	// 扩展字段（minidocker 特定）
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`

	// Phase 6: cgroup 路径
	// 格式: minidocker/<container-id>
	CgroupPath string `json:"cgroupPath,omitempty"`

	// Phase 7: 网络状态
	NetworkState *NetworkState `json:"networkState,omitempty"`

	// Phase 9: 快照路径（用于清理）
	SnapshotPath string `json:"snapshotPath,omitempty"`

	// Phase 9: 镜像引用（用于显示）
	ImageRef string `json:"imageRef,omitempty"`

	// 内部字段（不序列化）
	containerDir string
}

// NetworkState 表示容器的网络状态
type NetworkState struct {
	Mode          string        `json:"mode"`
	IPAddress     string        `json:"ipAddress,omitempty"`
	Gateway       string        `json:"gateway,omitempty"`
	MacAddress    string        `json:"macAddress,omitempty"`
	VethHost      string        `json:"vethHost,omitempty"`
	VethContainer string        `json:"vethContainer,omitempty"`
	PortMappings  []PortMapping `json:"portMappings,omitempty"`
}

// NewState 创建一个新的容器状态
func NewState(id, containerDir string) *ContainerState {
	return &ContainerState{
		OCIVersion:   OCIVersionCurrent,
		ID:           id,
		Status:       StatusCreating,
		Bundle:       containerDir,
		CreatedAt:    time.Now(),
		containerDir: containerDir,
	}
}

// LoadState 从容器目录加载状态
func LoadState(containerDir string) (*ContainerState, error) {
	statePath := filepath.Join(containerDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}

	var state ContainerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}

	state.containerDir = containerDir
	return &state, nil
}

// Save 保存状态到 state.json
func (s *ContainerState) Save() error {
	if s.containerDir == "" {
		return fmt.Errorf("container directory not set")
	}

	// Use a per-container lock to avoid concurrent writers clobbering state.
	lock, err := AcquireLock(s.containerDir)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	statePath := filepath.Join(s.containerDir, "state.json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	// 原子写入：先写临时文件，再重命名
	if err := fileutil.AtomicWriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	return nil
}

// Reload 从磁盘重新加载状态
func (s *ContainerState) Reload() error {
	if s.containerDir == "" {
		return fmt.Errorf("container directory not set")
	}

	newState, err := LoadState(s.containerDir)
	if err != nil {
		return err
	}

	// 更新字段
	s.OCIVersion = newState.OCIVersion
	s.ID = newState.ID
	s.Status = newState.Status
	s.Pid = newState.Pid
	s.Bundle = newState.Bundle
	s.CreatedAt = newState.CreatedAt
	s.StartedAt = newState.StartedAt
	s.FinishedAt = newState.FinishedAt
	s.ExitCode = newState.ExitCode
	s.CgroupPath = newState.CgroupPath       // Phase 6
	s.NetworkState = newState.NetworkState   // Phase 7
	s.SnapshotPath = newState.SnapshotPath   // Phase 9
	s.ImageRef = newState.ImageRef           // Phase 9

	return nil
}

// SetRunning 将状态设为 running 并记录 PID
func (s *ContainerState) SetRunning(pid int) error {
	s.Status = StatusRunning
	s.Pid = pid
	now := time.Now()
	s.StartedAt = &now
	return s.Save()
}

// SetStopped 将状态设为 stopped 并记录退出码
func (s *ContainerState) SetStopped(exitCode int) error {
	s.Status = StatusStopped
	now := time.Now()
	s.FinishedAt = &now
	s.ExitCode = &exitCode
	return s.Save()
}

// IsRunning 检查容器是否实际运行中。
// 不仅检查状态字段，还验证进程是否真实存在。
// 如果检测到进程已不存在（孤儿状态），会自动修正状态。
func (s *ContainerState) IsRunning() bool {
	if s.Status != StatusRunning {
		return false
	}

	if s.Pid == 0 {
		return false
	}

	// 检查进程是否实际存在
	// syscall.Kill(pid, 0) 不发送信号，仅检查进程是否存在
	if err := syscall.Kill(s.Pid, 0); err != nil {
		// ESRCH: 进程不存在，自动修正状态
		if err == syscall.ESRCH {
			s.Status = StatusStopped
			now := time.Now()
			s.FinishedAt = &now
			// 无法确定退出码，设为 -1
			exitCode := -1
			s.ExitCode = &exitCode
			_ = s.Save() // best effort
			return false
		}

		// 其他错误（例如 EPERM）并不一定代表进程不存在；保守认为仍在运行。
		return true
	}

	return true
}

// GetContainerDir 返回容器目录路径
func (s *ContainerState) GetContainerDir() string {
	return s.containerDir
}

// GetLogDir 返回日志目录路径
func (s *ContainerState) GetLogDir() string {
	return filepath.Join(s.containerDir, "logs")
}

// timePtr 返回时间指针（辅助函数）
func timePtr(t time.Time) *time.Time {
	return &t
}

// intPtr 返回整数指针（辅助函数）
func intPtr(i int) *int {
	return &i
}
