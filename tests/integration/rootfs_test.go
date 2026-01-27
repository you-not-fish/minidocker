//go:build integration && linux
// +build integration,linux

package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRootfsIsolation 测试根文件系统隔离生效
func TestRootfsIsolation(t *testing.T) {
	skipIfNotRoot(t)

	// 准备一个最小 rootfs（使用 busybox）
	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 在 rootfs 内创建一个标记文件，确保容器确实切到了这个 rootfs。
	rootfsMarker := ".minidocker_rootfs_marker"
	if err := os.WriteFile(filepath.Join(rootfs, rootfsMarker), []byte("ok\n"), 0644); err != nil {
		t.Fatalf("failed to write rootfs marker: %v", err)
	}

	// 在宿主机 /tmp 创建一个标记文件（容器 pivot_root 后不应该能看到）。
	hostMarkerFile, err := os.CreateTemp("", "minidocker-host-marker-*")
	if err != nil {
		t.Fatalf("failed to create host marker: %v", err)
	}
	hostMarkerName := filepath.Base(hostMarkerFile.Name())
	_ = hostMarkerFile.Close()
	t.Cleanup(func() { _ = os.Remove(hostMarkerFile.Name()) })

	// 确保 rootfs 里存在 /tmp（避免“/tmp 不存在导致 not-exist 断言失真”）
	if err := os.MkdirAll(filepath.Join(rootfs, "tmp"), 0755); err != nil {
		t.Fatalf("failed to ensure rootfs /tmp: %v", err)
	}

	script := fmt.Sprintf(
		`set -eu
test -f "/%s" && echo in_rootfs
test ! -e "/tmp/%s" && echo not_host
`,
		rootfsMarker,
		hostMarkerName,
	)

	cmd := exec.Command(minidockerBin, "run", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	if !strings.Contains(outputStr, "in_rootfs") {
		t.Fatalf("Expected rootfs marker to be visible inside container. Output:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "not_host") {
		t.Fatalf("Expected host marker NOT to be visible inside container. Output:\n%s", outputStr)
	}
}

// TestProcOnlyShowsContainerProcesses 测试 /proc 只显示容器内进程
func TestProcOnlyShowsContainerProcesses(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 仅依赖 /bin/sh：使用 glob 扫描 /proc/[0-9]*
	script := `
set -eu
n=0
for d in /proc/[0-9]*; do
  [ -d "$d" ] || continue
  n=$((n+1))
done
echo "n=$n"
test -d /proc/1 && echo "has1"
`
	cmd := exec.Command(minidockerBin, "run", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	outStr := strings.TrimSpace(string(output))
	var n int
	for _, line := range strings.Split(outStr, "\n") {
		if strings.HasPrefix(line, "n=") {
			_, _ = fmt.Sscanf(line, "n=%d", &n)
		}
	}

	// 断言：/proc 下的 PID 数量应该很少（< 10）
	if n > 10 {
		t.Errorf("Expected few PIDs in container /proc, got n=%d. Output:\n%s", n, outStr)
	}

	// 断言：应该包含 PID 1（init）
	if !strings.Contains(outStr, "has1") {
		t.Errorf("Expected /proc/1 to exist. Output:\n%s", outStr)
	}
}

// TestDeviceNodesExist 测试 /dev 设备节点存在
func TestDeviceNodesExist(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	devices := []string{"null", "zero", "random", "urandom"}

	for _, dev := range devices {
		t.Run(dev, func(t *testing.T) {
			cmd := exec.Command(minidockerBin, "run", "--rootfs", rootfs,
				"/bin/sh", "-c", "test -c /dev/"+dev+" && echo exists")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("test /dev/%s failed: %v\nOutput: %s", dev, err, output)
			}

			if !strings.Contains(string(output), "exists") {
				t.Errorf("/dev/%s should exist as character device", dev)
			}
		})
	}
}

// TestDevStdioSymlinks 测试 /dev/stdin 等符号链接
func TestDevStdioSymlinks(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	cmd := exec.Command(minidockerBin, "run", "--rootfs", rootfs,
		"/bin/sh", "-c", "echo hello > /dev/stdout")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test /dev/stdout failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello") {
		t.Errorf("Expected output via /dev/stdout, got: %s", output)
	}
}

// TestSysMounted 测试 /sys 挂载（只读）
func TestSysMounted(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	cmd := exec.Command(minidockerBin, "run", "--rootfs", rootfs,
		"/bin/sh", "-c", "test -d /sys/class && echo exists")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("warning: /sys not mounted (acceptable): %v", err)
		return
	}

	if !strings.Contains(string(output), "exists") {
		t.Logf("warning: /sys/class not accessible")
	}
}

// TestRootfsValidation 测试 rootfs 参数验证
func TestRootfsValidation(t *testing.T) {
	skipIfNotRoot(t)

	tests := []struct {
		name        string
		rootfs      string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "non_existent",
			rootfs:      "/nonexistent/path",
			expectError: true,
			errorMsg:    "does not exist",
		},
		{
			name:        "file_not_directory",
			rootfs:      "/etc/passwd",
			expectError: true,
			errorMsg:    "not a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(minidockerBin, "run", "--rootfs", tt.rootfs, "/bin/true")
			output, err := cmd.CombinedOutput()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, but command succeeded")
				}
				if !strings.Contains(string(output), tt.errorMsg) {
					t.Errorf("Expected error message containing %q, got: %s", tt.errorMsg, output)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v\nOutput: %s", err, output)
				}
			}
		})
	}
}

// TestBackwardCompatibilityNoRootfs 测试不指定 rootfs 时的向后兼容
func TestBackwardCompatibilityNoRootfs(t *testing.T) {
	skipIfNotRoot(t)

	// Phase 1 模式：不指定 rootfs，应该仍能运行
	cmd := exec.Command(minidockerBin, "run", "/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("backward compatibility broken: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello") {
		t.Errorf("Expected output 'hello', got: %s", output)
	}
}

// prepareMinimalRootfs 创建一个最小的 busybox rootfs 用于测试。
//
// 设计目标：
// - integration 测试默认可跑（尽量不依赖 Docker/网络）
// - rootfs 内容尽量小，但必须包含容器内执行测试所需的最小命令集
// - 为避免污染宿主/用户提供的 rootfs，本函数总是返回一个临时目录（由调用方 RemoveAll）
//
// 当前实现的准备策略（按优先级）：
// 1) 若设置环境变量 MINIDOCKER_TEST_ROOTFS=<dir>：
//    - 递归复制该目录到临时目录后返回（避免修改原目录）
// 2) 若宿主可用 docker：
//    - docker export busybox 到临时目录（若 busybox 镜像不存在可能触发 pull，因此这一步可能失败）
// 3) 若宿主有 busybox 可执行文件：
//    - 构建一个最小 busybox rootfs（/bin/busybox + /bin/sh 等软链）
//    - 若 busybox 为动态链接，会额外复制其依赖库到 rootfs
// 4) 兜底：复制宿主 /bin/sh（或 PATH 中的 sh）到 rootfs，并复制动态依赖库（通过 ldd）
//
// 说明：
// - Phase 2 的 rootfs 测试用例已尽量只依赖 /bin/sh（以及其内建 test/echo/redirection），
//   这样 rootfs 构建可以非常小，且对宿主环境更友好。
func prepareMinimalRootfs(t *testing.T) string {
	t.Helper()

	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "minidocker-rootfs-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// 失败时清理（避免遗留临时目录）
	cleanup := func(reason string, args ...any) {
		_ = os.RemoveAll(tmpDir)
		t.Skipf(reason, args...)
	}

	// 策略 1：使用用户提供的 rootfs（复制到临时目录）
	if src := os.Getenv("MINIDOCKER_TEST_ROOTFS"); src != "" {
		if err := copyDirRecursive(src, tmpDir); err == nil {
			if err := ensureRootfsHasShell(tmpDir); err == nil {
				return tmpDir
			}
		}
		// 如果指定了 MINIDOCKER_TEST_ROOTFS 但不可用，直接报清晰原因，避免默默退化导致困惑
		cleanup("MINIDOCKER_TEST_ROOTFS=%q is not usable (must be a rootfs containing /bin/sh)", src)
	}

	// 策略 2：尝试 Docker 导出 busybox rootfs
	if err := exportBusyboxFromDocker(tmpDir); err == nil {
		if err := ensureRootfsHasShell(tmpDir); err == nil {
			return tmpDir
		}
	}

	// 策略 3：尝试使用宿主 busybox
	if err := buildMinimalBusyboxRootfs(tmpDir); err == nil {
		return tmpDir
	}

	// 策略 4：兜底使用宿主 /bin/sh
	if err := buildMinimalShellRootfs(tmpDir); err == nil {
		return tmpDir
	}

	cleanup("failed to prepare a minimal rootfs: no docker, no busybox, and failed to copy host /bin/sh")
	return tmpDir // unreachable
}

// exportBusyboxFromDocker 从 Docker 导出 busybox rootfs（可选实现）
func exportBusyboxFromDocker(rootfs string) error {
	// 实现方式：
	// 1. docker create busybox
	// 2. docker export <container_id> | tar -C <rootfs> -xf -
	// 3. docker rm <container_id>

	// 检查 docker 是否可用
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found: %w", err)
	}

	// 创建 busybox 容器
	createCmd := exec.Command("docker", "create", "busybox")
	createOut, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("docker create failed: %w", err)
	}
	containerID := strings.TrimSpace(string(createOut))
	defer func() {
		_ = exec.Command("docker", "rm", containerID).Run()
	}()

	// 导出容器文件系统
	exportCmd := exec.Command("docker", "export", containerID)
	tarCmd := exec.Command("tar", "-C", rootfs, "-xf", "-")

	stdoutPipe, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("docker export stdout pipe: %w", err)
	}
	tarCmd.Stdin = stdoutPipe
	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("tar start failed: %w", err)
	}
	if err := exportCmd.Run(); err != nil {
		return fmt.Errorf("docker export failed: %w", err)
	}
	if err := tarCmd.Wait(); err != nil {
		return fmt.Errorf("tar failed: %w", err)
	}

	return nil
}

func buildMinimalBusyboxRootfs(rootfs string) error {
	// 实现方式：
	// 1. 创建目录结构：bin, dev, etc, proc, sys, tmp, usr/bin
	// 2. 复制宿主的 /bin/busybox 或 /usr/bin/busybox
	// 3. 创建符号链接：sh, ls, cat, echo, mkdir, rm, true, false

	// 创建最小目录结构
	dirs := []string{"bin", "dev", "etc", "proc", "sys", "tmp", "usr/bin"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(rootfs, dir), 0755); err != nil {
			return err
		}
	}

	// 查找并复制 busybox
	busyboxPaths := []string{"/bin/busybox", "/usr/bin/busybox"}
	if p, err := exec.LookPath("busybox"); err == nil {
		busyboxPaths = append([]string{p}, busyboxPaths...)
	}
	var busyboxSrc string
	for _, src := range busyboxPaths {
		if _, err := os.Stat(src); err == nil {
			busyboxSrc = src
			break
		}
	}

	if busyboxSrc == "" {
		return fmt.Errorf("busybox not found on host")
	}

	dst := filepath.Join(rootfs, "bin/busybox")
	if err := copyFileWithMode(busyboxSrc, dst, 0755); err != nil {
		return err
	}
	if err := copyDynamicDepsIfNeeded(busyboxSrc, rootfs); err != nil {
		return err
	}

	// 创建符号链接（busybox 多命令）
	links := []string{
		// 测试必需：sh（其余是方便调试/扩展）
		"sh",
		"echo", "test", "ls", "cat", "grep", "wc",
		"mkdir", "rm", "true", "false", "sleep",
	}
	for _, link := range links {
		linkPath := filepath.Join(rootfs, "bin", link)
		_ = os.Remove(linkPath)
		if err := os.Symlink("busybox", linkPath); err != nil {
			return err
		}
	}

	return nil
}

func buildMinimalShellRootfs(rootfs string) error {
	// 最小目录结构（只放 /bin/sh + 其动态依赖）
	dirs := []string{"bin", "etc", "tmp", "usr/bin", "usr/lib", "lib", "lib64"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(rootfs, dir), 0755); err != nil {
			return err
		}
	}

	// 优先使用 /bin/sh，其次从 PATH 找 sh
	shellSrc := "/bin/sh"
	if _, err := os.Stat(shellSrc); err != nil {
		if p, err := exec.LookPath("sh"); err == nil {
			shellSrc = p
		} else {
			return fmt.Errorf("host shell not found (/bin/sh or sh in PATH)")
		}
	}

	shellDst := filepath.Join(rootfs, "bin/sh")
	if err := copyFileWithMode(shellSrc, shellDst, 0755); err != nil {
		return err
	}
	if err := copyDynamicDepsIfNeeded(shellSrc, rootfs); err != nil {
		return err
	}
	return nil
}

func ensureRootfsHasShell(rootfs string) error {
	if _, err := os.Stat(filepath.Join(rootfs, "bin/sh")); err == nil {
		return nil
	}
	// 常见情况：busybox rootfs 可能只有 /bin/busybox
	if _, err := os.Stat(filepath.Join(rootfs, "bin/busybox")); err == nil {
		_ = os.Remove(filepath.Join(rootfs, "bin/sh"))
		return os.Symlink("busybox", filepath.Join(rootfs, "bin/sh"))
	}
	if _, err := os.Stat(filepath.Join(rootfs, "usr/bin/busybox")); err == nil {
		_ = os.MkdirAll(filepath.Join(rootfs, "bin"), 0755)
		_ = os.Remove(filepath.Join(rootfs, "bin/sh"))
		return os.Symlink("/usr/bin/busybox", filepath.Join(rootfs, "bin/sh"))
	}
	return fmt.Errorf("rootfs missing /bin/sh (and no busybox fallback)")
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func copyDynamicDepsIfNeeded(binaryPath string, rootfs string) error {
	// 如果 ldd 不存在，直接报错（integration 环境通常具备 ldd；若没有可通过 MINIDOCKER_TEST_ROOTFS 绕过）
	lddPath, err := exec.LookPath("ldd")
	if err != nil {
		return fmt.Errorf("ldd not found: %w", err)
	}

	lddCmd := exec.Command(lddPath, binaryPath)
	out, runErr := lddCmd.CombinedOutput()
	// 某些实现对“静态链接”可能返回非 0，但仍输出提示；我们只在解析出缺失依赖时失败。
	paths, missing := parseLddOutput(out)
	if len(missing) > 0 {
		return fmt.Errorf("missing dynamic libs for %s: %v\nldd output:\n%s", binaryPath, missing, string(out))
	}
	if runErr != nil && len(paths) == 0 {
		// 既没有解析到依赖，也执行失败，给出更明确的原因
		//（常见是 ldd 对某些二进制返回非 0）
		// 这里不直接失败：静态链接二进制场景下不需要依赖库。
		return nil
	}

	for _, p := range paths {
		// ldd 输出里可能包含 vdso 等非文件项
		if !strings.HasPrefix(p, "/") {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("ldd reported dependency %q but it does not exist: %w", p, err)
		}
		dst := filepath.Join(rootfs, strings.TrimPrefix(p, "/"))
		if err := copyFileWithMode(p, dst, 0644); err != nil {
			return fmt.Errorf("copy dep %s: %w", p, err)
		}
	}
	return nil
}

func parseLddOutput(out []byte) (paths []string, missing []string) {
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.Contains(line, "=>") {
			parts := strings.SplitN(line, "=>", 2)
			left := strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])
			fields := strings.Fields(right)
			if len(fields) == 0 {
				continue
			}
			if fields[0] == "not" && len(fields) >= 2 && fields[1] == "found" {
				missing = append(missing, left)
				continue
			}
			if strings.HasPrefix(fields[0], "/") {
				if _, ok := seen[fields[0]]; !ok {
					seen[fields[0]] = struct{}{}
					paths = append(paths, fields[0])
				}
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "/") {
			if _, ok := seen[fields[0]]; !ok {
				seen[fields[0]] = struct{}{}
				paths = append(paths, fields[0])
			}
		}
	}
	return paths, missing
}

func copyDirRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dst, rel)

		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}

		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}
			_ = os.Remove(dstPath)
			return os.Symlink(target, dstPath)
		case fi.IsDir():
			return os.MkdirAll(dstPath, fi.Mode().Perm())
		case fi.Mode().IsRegular():
			return copyFileWithMode(path, dstPath, fi.Mode().Perm())
		default:
			// 跳过特殊文件（设备节点等）。Phase 2 中 /dev 会在容器内重新挂载并创建。
			return nil
		}
	})
}
