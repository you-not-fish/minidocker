//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// setupRootfs 切换到指定的根文件系统并设置必需的挂载点。
// 必须在 setupContainerEnvironment() 中
// 最早调用（在 hostname/proc 之前）。
//
// 实现对齐 runc 的 pivot_root 标准做法：
// 1. 确保 rootfs 是私有挂载（防止传播）
// 2. bind mount rootfs 到自己（满足 pivot_root 要求）
// 3. 创建 old_root 临时目录
// 4. pivot_root(new_root, old_root)
// 5. chdir("/") 进入新根
// 6. 递归 umount old_root
// 7. rmdir old_root
//
// 参考：runc libcontainer/rootfs_linux.go:pivotRoot()
func setupRootfs(config *ContainerConfig) error {
	if config.Rootfs == "" {
		return nil // Phase 1 兼容：无 rootfs 时跳过
	}

	rootfs := config.Rootfs

	// 1. 确保 rootfs 存在且可访问
	if err := validateRootfs(rootfs); err != nil {
		return err
	}

	// 2. 先将当前 mount namespace 的传播设为 private（rprivate）
	// 避免后续的 bind mount/pivot_root 等 mount 操作通过 shared mount 传播回宿主机。
	// 这一步应尽量早做（对齐 runc 的常见做法）。
	if err := setMountPropagation(); err != nil {
		return err
	}

	// 3. 将 rootfs bind mount 到自己（pivot_root 要求）
	if err := bindMountRootfs(rootfs); err != nil {
		return fmt.Errorf("bind mount rootfs: %w", err)
	}

	// Phase 10: 先把用户 mounts 挂到 rootfs/<target> 上，再 pivot_root。
	// 这样 mount(2) 的 source（宿主路径/卷路径）仍可解析，同时 pivot_root 后容器内可见路径为 /<target>。
	// 这对齐 runc 的常见实现方式：在 pivot_root 前把 mounts 准备到 newRoot 下。
	if len(config.Mounts) > 0 {
		if err := setupMounts(rootfs, config.Mounts); err != nil {
			return fmt.Errorf("setup mounts: %w", err)
		}
	}

	// 4. 执行 pivot_root 切换根
	if err := pivotRoot(rootfs); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	// 5. 挂载必需的伪文件系统（按依赖顺序）
	if err := mountProc(); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}
	if err := mountDev(); err != nil {
		return fmt.Errorf("mount /dev: %w", err)
	}
	if err := mountSys(); err != nil {
		// /sys 失败降级为警告（不阻塞启动）
		fmt.Fprintf(os.Stderr, "warning: mount /sys failed: %v\n", err)
	}

	return nil
}

// validateRootfs 检查 rootfs 路径是否有效。
func validateRootfs(rootfs string) error {
	info, err := os.Stat(rootfs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("rootfs does not exist: %s", rootfs)
		}
		return fmt.Errorf("stat rootfs: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootfs is not a directory: %s", rootfs)
	}
	return nil
}

// bindMountRootfs 将 rootfs bind mount 到自己，满足 pivot_root 的前置条件。
// pivot_root 要求 new_root 必须是挂载点。
func bindMountRootfs(rootfs string) error {
	// 确保 rootfs 是绝对路径
	absRootfs, err := filepath.Abs(rootfs)
	if err != nil {
		return err
	}

	// bind mount: mount --bind <rootfs> <rootfs>
	// 这样 rootfs 就成为了一个挂载点
	if err := unix.Mount(absRootfs, absRootfs, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount %s to itself: %w", absRootfs, err)
	}

	// 将挂载设为私有，防止传播到宿主
	if err := unix.Mount("", absRootfs, "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("make rootfs private: %w", err)
	}

	return nil
}

// pivotRoot 执行根切换（对齐 runc 标准流程）。
func pivotRoot(rootfs string) error {
	// pivot_root 要求使用绝对路径
	absRootfs, err := filepath.Abs(rootfs)
	if err != nil {
		return err
	}

	// 创建 old_root 临时目录（放在新根内）
	// runc 使用 .pivot_root<random> 防止冲突（避免 rootfs 内恰好存在同名文件/目录）
	oldRoot, err := os.MkdirTemp(absRootfs, ".pivot_root")
	if err != nil {
		return fmt.Errorf("mkdir old_root: %w", err)
	}
	oldRootBase := filepath.Base(oldRoot)

	// 执行 pivot_root 系统调用
	// pivot_root(new_root, put_old)
	if err := unix.PivotRoot(absRootfs, oldRoot); err != nil {
		return fmt.Errorf("pivot_root syscall: %w", err)
	}

	// 切换工作目录到新根
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir to new root: %w", err)
	}

	// 卸载 old_root（pivot_root 之后它会出现在新根的顶层）
	oldRoot = "/" + oldRootBase
	if err := unmountOldRoot(oldRoot); err != nil {
		return fmt.Errorf("unmount old_root: %w", err)
	}

	// 删除 old_root 目录
	if err := os.Remove(oldRoot); err != nil {
		return fmt.Errorf("remove old_root: %w", err)
	}

	return nil
}

// unmountOldRoot 递归卸载旧根（防止容器逃逸）。
func unmountOldRoot(oldRoot string) error {
	// MNT_DETACH: 延迟卸载（即使有进程在使用也能卸载）
	// 这是 runc 的标准做法，确保清理彻底
	if err := unix.Unmount(oldRoot, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("umount old_root: %w", err)
	}
	return nil
}

// mountProc 为容器的 PID namespace 挂载一个新的 /proc 文件系统。
// 这允许 'ps'、'/proc/self/*' 等在容器内正确工作。
//
// 注意：这是从 init.go 迁移过来的，逻辑不变，但调用位置改为 pivot_root 之后。
func mountProc() error {
	target := "/proc"

	// 确保 /proc 目录存在
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	// 首先，尝试卸载任何现有的 /proc
	// 忽略错误，因为它可能未挂载
	_ = unix.Unmount(target, unix.MNT_DETACH)

	// 挂载新的 proc 文件系统
	flags := uintptr(unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV)
	if err := unix.Mount("proc", target, "proc", flags, ""); err != nil {
		return fmt.Errorf("mount proc: %w", err)
	}

	return nil
}

// mountDev 挂载 /dev（Phase 2: 最小 tmpfs + 手动创建设备节点）。
// Phase 14 可升级为 devtmpfs 或更完整的设备管理。
func mountDev() error {
	target := "/dev"

	// 确保 /dev 目录存在
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	// 挂载 tmpfs 到 /dev
	if err := unix.Mount("tmpfs", target, "tmpfs", unix.MS_NOSUID|unix.MS_STRICTATIME, "mode=0755"); err != nil {
		return fmt.Errorf("mount tmpfs: %w", err)
	}

	// 创建必需的设备节点（最小集合）
	devices := []struct {
		path string
		mode uint32
		dev  int
	}{
		{"/dev/null", unix.S_IFCHR | 0666, int(unix.Mkdev(1, 3))},
		{"/dev/zero", unix.S_IFCHR | 0666, int(unix.Mkdev(1, 5))},
		{"/dev/full", unix.S_IFCHR | 0666, int(unix.Mkdev(1, 7))},
		{"/dev/random", unix.S_IFCHR | 0666, int(unix.Mkdev(1, 8))},
		{"/dev/urandom", unix.S_IFCHR | 0666, int(unix.Mkdev(1, 9))},
		{"/dev/tty", unix.S_IFCHR | 0666, int(unix.Mkdev(5, 0))},
	}

	for _, d := range devices {
		if err := unix.Mknod(d.path, d.mode, d.dev); err != nil {
			// best-effort: 个别设备失败不阻塞（可能已存在或无权限）
			fmt.Fprintf(os.Stderr, "warning: mknod %s: %v\n", d.path, err)
		}
	}

	// 创建标准符号链接
	symlinks := []struct{ old, new string }{
		{"/proc/self/fd", "/dev/fd"},
		{"/proc/self/fd/0", "/dev/stdin"},
		{"/proc/self/fd/1", "/dev/stdout"},
		{"/proc/self/fd/2", "/dev/stderr"},
	}

	for _, s := range symlinks {
		// 删除可能存在的旧链接
		_ = os.Remove(s.new)
		if err := os.Symlink(s.old, s.new); err != nil {
			fmt.Fprintf(os.Stderr, "warning: symlink %s -> %s: %v\n", s.old, s.new, err)
		}
	}

	// 创建 /dev/pts 子目录（为 Phase 5 PTY 准备）
	ptsDir := "/dev/pts"
	if err := os.MkdirAll(ptsDir, 0755); err != nil {
		return err
	}

	// 挂载 devpts（伪终端）
	if err := unix.Mount("devpts", ptsDir, "devpts", unix.MS_NOSUID|unix.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		// devpts 失败降级为警告（Phase 5 才强依赖）
		fmt.Fprintf(os.Stderr, "warning: mount devpts: %v\n", err)
	}

	// 创建 /dev/ptmx 符号链接
	ptmx := "/dev/ptmx"
	_ = os.Remove(ptmx)
	if err := os.Symlink("pts/ptmx", ptmx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: symlink /dev/ptmx: %v\n", err)
	}

	return nil
}

// mountSys 挂载 /sys（只读）。
func mountSys() error {
	target := "/sys"

	// 确保 /sys 目录存在
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	// 挂载 sysfs 为只读
	flags := unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV | unix.MS_RDONLY
	if err := unix.Mount("sysfs", target, "sysfs", uintptr(flags), ""); err != nil {
		return fmt.Errorf("mount sysfs: %w", err)
	}

	return nil
}
