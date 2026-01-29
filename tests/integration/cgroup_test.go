//go:build integration && linux
// +build integration,linux

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Phase 6: cgroup v2 集成测试
//
// 测试环境要求：
// - Linux 内核 >= 5.10（推荐）
// - cgroup v2 统一层级
// - Root 权限
// - minidocker 二进制文件

// skipIfNotCgroupV2 跳过非 cgroup v2 系统
func skipIfNotCgroupV2(t *testing.T) {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); os.IsNotExist(err) {
		t.Skip("This test requires cgroup v2 (unified hierarchy)")
	}
}

func hasController(t *testing.T, controller string) bool {
	data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		t.Fatalf("Failed to read cgroup.controllers: %v", err)
	}
	for _, c := range strings.Fields(string(data)) {
		if c == controller {
			return true
		}
	}
	return false
}

func skipIfControllerMissing(t *testing.T, controller string) {
	if !hasController(t, controller) {
		t.Skipf("controller %q not available in cgroup.controllers", controller)
	}
}

func readCgroupFile(t *testing.T, containerID, filename string) string {
	t.Helper()
	p := filepath.Join("/sys/fs/cgroup", "minidocker", containerID, filename)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", p, err)
	}
	return strings.TrimSpace(string(data))
}

func cleanupContainer(t *testing.T, stateRoot, containerID string) {
	t.Helper()
	_ = exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
	_ = exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
}

func waitForGone(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("path still exists after %s: %s", timeout, path)
}

func findSingleContainerID(t *testing.T, stateRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(stateRoot, "containers"))
	if err != nil {
		t.Fatalf("Failed to read containers directory: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("Expected exactly 1 container directory, got %d: %v", len(entries), names)
	}
	return entries[0].Name()
}

// TestCgroupV2Available 检测 cgroup v2 是否可用
func TestCgroupV2Available(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	// 检查 cgroup v2 统一层级
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); os.IsNotExist(err) {
		t.Fatal("cgroup v2 is not available on this system")
	}

	// 检查必要的控制器
	data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		t.Fatalf("Failed to read cgroup.controllers: %v", err)
	}

	controllers := strings.Fields(string(data))
	required := []string{"memory", "cpu", "pids"}
	for _, r := range required {
		found := false
		for _, c := range controllers {
			if c == r {
				found = true
				break
			}
		}
		if !found {
			t.Logf("Warning: controller %s not available (some tests may be skipped)", r)
		}
	}

	t.Logf("Available controllers: %v", controllers)
}

// TestMemoryLimit 测试内存限制生效
func TestMemoryLimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "memory")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 使用后台模式保持 cgroup 存在，便于从宿主侧读取限制配置
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	memoryMax := readCgroupFile(t, containerID, "memory.max")
	expected := strconv.FormatInt(64*1024*1024, 10) // 64MB in bytes
	if memoryMax != expected {
		t.Errorf("Expected memory.max=%s, got=%s", expected, memoryMax)
	}
}

// TestCPULimit 测试 CPU 限制生效
func TestCPULimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "cpu")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--cpus", "0.5",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	cpuMax := readCgroupFile(t, containerID, "cpu.max")
	parts := strings.Fields(cpuMax)
	if len(parts) != 2 {
		t.Fatalf("Unexpected cpu.max format: %s", cpuMax)
	}

	quota, _ := strconv.ParseInt(parts[0], 10, 64)
	period, _ := strconv.ParseInt(parts[1], 10, 64)

	// 0.5 CPU = 50000 / 100000
	expectedQuota := int64(50000)
	expectedPeriod := int64(100000)

	if quota != expectedQuota || period != expectedPeriod {
		t.Errorf("Expected cpu.max=%d %d, got=%d %d",
			expectedQuota, expectedPeriod, quota, period)
	}
}

// TestPidsLimit 测试进程数限制
func TestPidsLimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "pids")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--pids-limit", "10",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	pidsMax := readCgroupFile(t, containerID, "pids.max")
	if pidsMax != "10" {
		t.Errorf("Expected pids.max=10, got=%s", pidsMax)
	}
}

// TestPidsLimitEnforcement 测试进程数限制实际生效
func TestPidsLimitEnforcement(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "pids")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 设置 pids 限制为 5，尝试在容器内 fork 更多进程，并通过宿主侧读取 pids.events 验证发生过触顶事件。
	script := `
set +e
i=0
while [ "$i" -lt 20 ]; do
  sleep 10 &
  i=$((i+1))
done
sleep 10
`
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--pids-limit", "5",
		"--rootfs", rootfs,
		"/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 给容器一点时间尝试 fork
	time.Sleep(300 * time.Millisecond)

	events := readCgroupFile(t, containerID, "pids.events")
	var maxEvents int64 = -1
	for _, line := range strings.Split(events, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "max" {
			v, parseErr := strconv.ParseInt(fields[1], 10, 64)
			if parseErr == nil {
				maxEvents = v
			}
			break
		}
	}
	if maxEvents < 0 {
		t.Fatalf("Unexpected pids.events format: %q", events)
	}
	if maxEvents == 0 {
		t.Fatalf("Expected pids.events max > 0 (limit enforcement), got 0. pids.events:\n%s", events)
	}
}

// TestCgroupCleanup 测试前台模式下 cgroup 会在容器退出后被清理
func TestCgroupCleanup(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "memory")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 前台运行带资源限制的容器（会立即退出）
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := findSingleContainerID(t, stateRoot)
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 容器退出后 cgroup 目录应被删除
	cgroupDir := filepath.Join("/sys/fs/cgroup/minidocker", containerID)
	if err := waitForGone(cgroupDir, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestDetachedCgroupCleanup 测试后台模式下 cgroup 清理
func TestDetachedCgroupCleanup(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "memory")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行后台容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sleep", "1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) != 64 {
		t.Fatalf("Invalid container ID: %s", containerID)
	}
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查 cgroup 目录是否创建
	cgroupPath := filepath.Join("/sys/fs/cgroup/minidocker", containerID)
	if _, err := os.Stat(cgroupPath); os.IsNotExist(err) {
		t.Errorf("Cgroup directory should exist while container is running: %s", cgroupPath)
	}

	// 等待容器退出
	time.Sleep(2 * time.Second)

	// 检查 cgroup 目录是否已清理
	if _, err := os.Stat(cgroupPath); !os.IsNotExist(err) {
		t.Errorf("Cgroup directory should be removed after container exit: %s", cgroupPath)
	}
}

// TestCombinedLimits 测试组合资源限制
func TestCombinedLimits(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)
	skipIfControllerMissing(t, "memory")
	skipIfControllerMissing(t, "cpu")
	skipIfControllerMissing(t, "pids")

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-m", "128m",
		"--cpus", "1",
		"--pids-limit", "50",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 验证内存限制
	expectedMemory := strconv.FormatInt(128*1024*1024, 10) // 128MB
	if got := readCgroupFile(t, containerID, "memory.max"); got != expectedMemory {
		t.Errorf("Expected memory.max=%s, got=%s", expectedMemory, got)
	}

	// 验证 CPU 限制
	if got := readCgroupFile(t, containerID, "cpu.max"); strings.TrimSpace(got) != "100000 100000" {
		t.Errorf("Expected cpu.max=100000 100000, got=%s", got)
	}

	// 验证进程数限制
	if got := readCgroupFile(t, containerID, "pids.max"); strings.TrimSpace(got) != "50" {
		t.Errorf("Expected pids.max=50, got=%s", got)
	}
}

// TestNoCgroupWithoutLimits 测试无资源限制时不创建 cgroup
func TestNoCgroupWithoutLimits(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行不带资源限制的后台容器：此时不应该创建 /sys/fs/cgroup/minidocker/<id>
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	cgroupPath := filepath.Join("/sys/fs/cgroup/minidocker", containerID)
	if _, err := os.Stat(cgroupPath); err == nil {
		t.Fatalf("Cgroup directory should NOT exist when no limits are set: %s", cgroupPath)
	}
}

// TestInspectCgroupPath 测试 inspect 显示 cgroup 路径
func TestInspectCgroupPath(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行后台容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	defer func() {
		// 清理
		cleanupContainer(t, stateRoot, containerID)
	}()

	// 检查 inspect 输出
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 验证 cgroupPath 字段存在
	if !strings.Contains(string(inspectOutput), "cgroupPath") {
		t.Errorf("Expected cgroupPath in inspect output")
	}

	// 验证 cgroup 路径格式
	expectedPath := "minidocker/" + containerID
	if !strings.Contains(string(inspectOutput), expectedPath) {
		t.Errorf("Expected cgroupPath containing %s", expectedPath)
	}

	t.Logf("Inspect output:\n%s", inspectOutput)
}
