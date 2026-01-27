//go:build linux
// +build linux

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ContainerLock 提供容器状态操作的文件锁。
// 使用 flock(2) 实现进程间互斥。
type ContainerLock struct {
	path string
	file *os.File
}

// AcquireLock 获取容器目录的独占锁。
// 如果锁已被其他进程持有，会阻塞等待。
func AcquireLock(containerDir string) (*ContainerLock, error) {
	lockPath := filepath.Join(containerDir, "lock")

	// 创建或打开锁文件
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// 获取独占锁（阻塞）
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return &ContainerLock{
		path: lockPath,
		file: file,
	}, nil
}

// TryAcquireLock 尝试获取容器目录的独占锁。
// 如果锁已被其他进程持有，立即返回错误（非阻塞）。
func TryAcquireLock(containerDir string) (*ContainerLock, error) {
	lockPath := filepath.Join(containerDir, "lock")

	// 创建或打开锁文件
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// 尝试获取独占锁（非阻塞）
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("container is locked by another process")
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return &ContainerLock{
		path: lockPath,
		file: file,
	}, nil
}

// Release 释放锁
func (l *ContainerLock) Release() error {
	if l.file == nil {
		return nil
	}

	// 解锁
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		l.file.Close()
		return fmt.Errorf("release lock: %w", err)
	}

	// 关闭文件
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}

	l.file = nil
	return nil
}
